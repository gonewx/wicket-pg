// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/session/sessiontest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionStore is the pgx-backed adapter for the wicket session.Store port.
// Each session is one row keyed by session_id with the model serialized
// into the payload column. Further columns back real query paths and the
// atomic partial writes: subject_id (batch lookup and revocation by
// subject), client_ids (the authoritative source for ClientIDs, appended
// atomically and deduplicated), renewed and expires (the authoritative
// source for Renewed and Expires, written in place by RenewSession and
// TouchSession; expires also backs reclaim-only cleanup, expiry judgement
// lives on the core side). A zero-value Renewed or Expires is stored as
// NULL; a NULL expires means the session never expires.
type SessionStore struct {
	baseStore
}

// NewSessionStore assembles a session store on a host-owned pool. The pool
// is never created or closed here; a nil logger falls back to
// slog.Default() via newBase.
func NewSessionStore(pool *pgxpool.Pool, logger *slog.Logger) *SessionStore {
	return &SessionStore{baseStore: newBase(pool, logger)}
}

// GetSession returns an independent copy of the session for the id. A
// missing session fails with session.ErrSessionNotFound and a nil record;
// the result is never (nil, nil). The call is a single-point lookup by the
// primary key, and expired records are returned as-is — expiry judgement
// belongs to the core side, never to the read path.
func (s *SessionStore) GetSession(ctx context.Context, sessionID string) (*session.Record, error) {
	// The session suite is the one suite that requires a canceled read to
	// surface the exact missing sentinel. pgx fails a query under a
	// canceled context with a wrapped context.Canceled, which no error
	// mapping can turn into ErrSessionNotFound, so the check happens here.
	if err := ctx.Err(); err != nil {
		return nil, session.ErrSessionNotFound
	}
	rec, err := s.scanRecord(s.pool.QueryRow(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE session_id = $1", sessionID))
	if err != nil {
		return nil, mapReadErr(err, session.ErrSessionNotFound)
	}
	s.logger.Debug("session read", "session_id_prefix", handlePrefix(sessionID), "found", true)
	return rec, nil
}

// CreateSession writes a session under its id. The write is insert-only: an
// id that already exists fails with the wrapped unique violation and the
// stored record is left unchanged. subject_id is derived from the record's
// SubjectID (an empty subject is stored as NULL and never indexed), and
// expires from Expires (a zero value is stored as NULL). A nil ClientIDs is
// normalized to an empty array because the column is NOT NULL.
func (s *SessionStore) CreateSession(ctx context.Context, rec *session.Record) error {
	payload, err := s.codec.encode(rec)
	if err != nil {
		return fmt.Errorf("create session payload: %w", err)
	}
	var subjectID *string
	if rec.SubjectID != "" {
		subjectID = &rec.SubjectID
	}
	clientIDs := rec.ClientIDs
	if clientIDs == nil {
		clientIDs = []string{}
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO sessions (session_id, subject_id, client_ids, renewed, expires, payload) VALUES ($1, $2, $3, $4, $5, $6)",
		rec.SessionID, subjectID, clientIDs, expiresAtPtr(rec.Renewed), expiresAtPtr(rec.Expires), payload)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	s.logger.Debug("session stored", "session_id_prefix", handlePrefix(rec.SessionID))
	return nil
}

// UpdateSession replaces the whole session row for the id: payload,
// subject_id, client_ids, renewed and expires are refreshed together in one
// statement, so a Coordinator refresh of Renewed is persisted. Updating a
// session that is not present succeeds as a no-op.
func (s *SessionStore) UpdateSession(ctx context.Context, rec *session.Record) error {
	payload, err := s.codec.encode(rec)
	if err != nil {
		return fmt.Errorf("update session payload: %w", err)
	}
	var subjectID *string
	if rec.SubjectID != "" {
		subjectID = &rec.SubjectID
	}
	clientIDs := rec.ClientIDs
	if clientIDs == nil {
		clientIDs = []string{}
	}
	_, err = s.pool.Exec(ctx,
		"UPDATE sessions SET payload = $2, subject_id = $3, client_ids = $4, renewed = $5, expires = $6 WHERE session_id = $1",
		rec.SessionID, payload, subjectID, clientIDs, expiresAtPtr(rec.Renewed), expiresAtPtr(rec.Expires))
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	s.logger.Debug("session updated", "session_id_prefix", handlePrefix(rec.SessionID))
	return nil
}

// DeleteSession removes the session for the id. Deleting a session that is
// not present is a successful no-op.
func (s *SessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM sessions WHERE session_id = $1", sessionID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	s.logger.Debug("session removed", "session_id_prefix", handlePrefix(sessionID))
	return nil
}

// GetSessionsBySubjectID returns an independent copy of every session for
// the subject, in an empty but non-nil slice when none match. ClientIDs,
// Renewed and Expires come from their columns for every row, as in
// GetSession.
func (s *SessionStore) GetSessionsBySubjectID(ctx context.Context, subjectID string) ([]*session.Record, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE subject_id = $1", subjectID)
	if err != nil {
		return nil, fmt.Errorf("query sessions by subject: %w", err)
	}
	defer rows.Close()

	sessions := []*session.Record{}
	for rows.Next() {
		rec, err := s.scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("read session row: %w", err)
		}
		sessions = append(sessions, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read session rows: %w", err)
	}
	s.logger.Debug("sessions by subject read", "subject_id_prefix", handlePrefix(subjectID), "count", len(sessions))
	return sessions, nil
}

// DeleteSessionsBySubjectID removes every session of the subject and
// returns how many were removed. Rows with a NULL subject_id (empty
// subject, never indexed) cannot be hit by the equality match.
func (s *SessionStore) DeleteSessionsBySubjectID(ctx context.Context, subjectID string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM sessions WHERE subject_id = $1", subjectID)
	if err != nil {
		return 0, fmt.Errorf("delete sessions by subject: %w", err)
	}
	s.logger.Debug("sessions by subject removed", "subject_id_prefix", handlePrefix(subjectID), "count", tag.RowsAffected())
	return int(tag.RowsAffected()), nil
}

// AddClientID atomically appends a client id to the session's client_ids
// column, deduplicating in a single statement so concurrent appends cannot
// lose or duplicate entries. A session that is not present is a no-op.
func (s *SessionStore) AddClientID(ctx context.Context, sessionID, clientID string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE sessions SET client_ids = array_append(client_ids, $2) WHERE session_id = $1 AND NOT ($2 = ANY(client_ids))",
		sessionID, clientID)
	if err != nil {
		return fmt.Errorf("add client id to session: %w", err)
	}
	s.logger.Debug("client id appended", "session_id_prefix", handlePrefix(sessionID), "client_id_prefix", handlePrefix(clientID))
	return nil
}

// RenewSession sets only the session's Renewed and Expires in one UPDATE
// on the renewed and expires columns and returns an independent copy of the
// renewed record read back by the same statement. The payload, subject_id
// and client_ids are not written, so a client id appended by AddClientID
// after the caller read the record survives. Expiry is not judged: an
// expired session is renewed too. A missing session returns
// (nil, session.ErrSessionNotFound) and no row is created.
func (s *SessionStore) RenewSession(ctx context.Context, sessionID string, renewed, expires time.Time) (*session.Record, error) {
	rec, err := s.scanRecord(s.pool.QueryRow(ctx,
		"UPDATE sessions SET renewed = $2, expires = $3 WHERE session_id = $1 RETURNING "+sessionColumns,
		sessionID, expiresAtPtr(renewed), expiresAtPtr(expires)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, session.ErrSessionNotFound
		}
		return nil, fmt.Errorf("renew session: %w", err)
	}
	s.logger.Debug("session renewed", "session_id_prefix", handlePrefix(sessionID))
	return rec, nil
}

// TouchSession sets only the session's Renewed in one UPDATE on the renewed
// column. Every other field, including an Expires written concurrently by
// RenewSession and client ids appended by AddClientID, keeps its stored
// value. Expiry is not judged and the old Renewed is not compared. A
// missing session (zero matched rows; PostgreSQL counts matched rows, so a
// repeated identical value still counts) returns session.ErrSessionNotFound
// and no row is created.
func (s *SessionStore) TouchSession(ctx context.Context, sessionID string, renewed time.Time) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE sessions SET renewed = $2 WHERE session_id = $1",
		sessionID, expiresAtPtr(renewed))
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return session.ErrSessionNotFound
	}
	s.logger.Debug("session touched", "session_id_prefix", handlePrefix(sessionID))
	return nil
}

// DeleteExpired removes sessions whose expires column precedes cutoff,
// strictly (a session with expires == cutoff is kept), and returns how many
// were removed. Rows with a NULL expires (zero-value Expires, never
// expires) are never touched. It reclaims space only; expiry judgement
// remains on the core side, so no clock function appears in the SQL.
func (s *SessionStore) DeleteExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM sessions WHERE expires IS NOT NULL AND expires < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *SessionStore) ConformsTo() string {
	return sessiontest.SuiteVersion
}

// sessionColumns is the select list every session read shares with
// scanRecord.
const sessionColumns = "client_ids, renewed, expires, payload"

// scanRecord decodes one row of sessionColumns into an independent record.
// The payload is a write-time snapshot; client_ids, renewed and expires are
// the authoritative sources for ClientIDs, Renewed and Expires because
// AddClientID, RenewSession and TouchSession write only those columns. A
// NULL renewed or expires is the zero time. A scan error is returned as-is
// so callers can recognize pgx.ErrNoRows.
func (s *SessionStore) scanRecord(row pgx.Row) (*session.Record, error) {
	var clientIDs []string
	var renewed, expires *time.Time
	var payload []byte
	if err := row.Scan(&clientIDs, &renewed, &expires, &payload); err != nil {
		return nil, err
	}
	rec := new(session.Record)
	if err := s.codec.decode(payload, rec); err != nil {
		return nil, fmt.Errorf("decode session payload: %w", err)
	}
	rec.ClientIDs = clientIDs
	rec.Renewed = timeOrZero(renewed)
	rec.Expires = timeOrZero(expires)
	return rec, nil
}

// timeOrZero is the read-side inverse of expiresAtPtr: NULL becomes the
// zero time, and a stored instant is returned in UTC rather than in the
// process-local zone pgx decodes timestamptz into.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}
