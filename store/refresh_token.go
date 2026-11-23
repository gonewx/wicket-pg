// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/gonewx/wicket/storage/storagetest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RefreshTokenStore is the pgx-backed adapter for the wicket
// storage.RefreshTokenStore port. Records live in the refresh_tokens table
// with the model serialized into the payload column and the concurrency
// guard version mirrored in its own column; expires_at is derived at write
// time and reclaimed only by an explicit RemoveExpired call.
type RefreshTokenStore struct {
	baseStore
}

// NewRefreshTokenStore assembles a refresh token store on a host-owned
// pool. The pool is never created or closed here; a nil logger falls back
// to slog.Default() via newBase.
func NewRefreshTokenStore(pool *pgxpool.Pool, logger *slog.Logger) *RefreshTokenStore {
	return &RefreshTokenStore{baseStore: newBase(pool, logger)}
}

// StoreRefreshToken writes a token under the caller-supplied handle. The
// write is insert-only: a handle that already exists fails with
// storage.ErrDuplicateHandle and the stored record is left unchanged. The
// stored copy carries version 1 and the caller's token.Version is written
// back with the same value; the caller object is untouched on failure. The
// token's client and session are written to the client_id / session_id
// locator columns (empty values as NULL).
func (s *RefreshTokenStore) StoreRefreshToken(ctx context.Context, handle string, token *models.RefreshToken) error {
	enc := *token
	enc.Version = 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("store refresh token payload: %w", err)
	}
	expiresAt := token.CreationTime.Add(time.Duration(token.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO refresh_tokens (handle, client_id, session_id, expires_at, version, payload) VALUES ($1, $2, $3, $4, 1, $5)",
		handle, nullIfEmpty(token.ClientId), nullIfEmpty(token.SessionId), expiresAt, payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	token.Version = 1
	s.logger.Debug("refresh token stored", "handle_prefix", handlePrefix(handle))
	return nil
}

// GetRefreshToken returns an independent copy of the stored token. The
// caller may mutate the returned value without affecting the store. A
// missing handle fails with storage.ErrNotFound; the record is never
// (nil, nil).
func (s *RefreshTokenStore) GetRefreshToken(ctx context.Context, handle string) (*models.RefreshToken, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM refresh_tokens WHERE handle = $1", handle).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	token := new(models.RefreshToken)
	if err := s.codec.decode(payload, token); err != nil {
		return nil, fmt.Errorf("decode refresh token payload: %w", err)
	}
	s.logger.Debug("refresh token read", "handle_prefix", handlePrefix(handle), "found", true)
	return token, nil
}

// UpdateRefreshToken replaces the stored token under an optimistic
// concurrency check expressed as a single conditional UPDATE: the row is
// modified only when the stored version equals expectedVersion, and the new
// stored copy carries expectedVersion+1, written back to the caller's
// token.Version. A zero-row outcome is re-checked for existence: a missing
// handle fails with storage.ErrNotFound, a version mismatch with
// storage.ErrVersionConflict. Either failure leaves the stored record and
// the caller object untouched.
func (s *RefreshTokenStore) UpdateRefreshToken(ctx context.Context, handle string, token *models.RefreshToken, expectedVersion int) error {
	enc := *token
	enc.Version = expectedVersion + 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("update refresh token payload: %w", err)
	}
	expiresAt := token.CreationTime.Add(time.Duration(token.Lifetime) * time.Second)
	tag, err := s.pool.Exec(ctx,
		"UPDATE refresh_tokens SET version = version + 1, expires_at = $2, payload = $3, client_id = $5, session_id = $6 "+
			"WHERE handle = $1 AND version = $4",
		handle, expiresAt, payload, expectedVersion, nullIfEmpty(token.ClientId), nullIfEmpty(token.SessionId))
	if err != nil {
		return fmt.Errorf("update refresh token: %w", err)
	}
	if tag.RowsAffected() == 1 {
		token.Version = expectedVersion + 1
		s.logger.Debug("refresh token updated", "handle_prefix", handlePrefix(handle))
		return nil
	}
	var one int
	err = s.pool.QueryRow(ctx,
		"SELECT 1 FROM refresh_tokens WHERE handle = $1", handle).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("recheck refresh token existence: %w", err)
	}
	return storage.ErrVersionConflict
}

// RemoveRefreshToken deletes the token under handle. Removing a handle
// that is not present is a successful no-op.
func (s *RefreshTokenStore) RemoveRefreshToken(ctx context.Context, handle string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM refresh_tokens WHERE handle = $1", handle)
	if err != nil {
		return fmt.Errorf("remove refresh token: %w", err)
	}
	s.logger.Debug("refresh token removed", "handle_prefix", handlePrefix(handle))
	return nil
}

// RemoveRefreshTokensByClient deletes every refresh token whose ClientId
// equals clientId in one DELETE on the refresh_tokens.client_id locator
// column and returns how many rows were removed. Zero matches return (0,
// nil). An empty clientId returns (0, nil) without issuing SQL, so records
// without a client are never selected.
func (s *RefreshTokenStore) RemoveRefreshTokensByClient(ctx context.Context, clientId string) (int, error) {
	if clientId == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM refresh_tokens WHERE client_id = $1", clientId)
	if err != nil {
		return 0, fmt.Errorf("remove refresh tokens by client: %w", err)
	}
	n := int(tag.RowsAffected())
	s.logger.Debug("refresh tokens removed by client", "client_prefix", handlePrefix(clientId), "count", n)
	return n, nil
}

// RemoveRefreshTokensBySession deletes every refresh token whose SessionId
// equals sessionId in one DELETE on the refresh_tokens.session_id locator
// column and returns how many rows were removed. Zero matches return (0,
// nil). An empty sessionId returns (0, nil) without issuing SQL, so records
// without a session are never selected.
func (s *RefreshTokenStore) RemoveRefreshTokensBySession(ctx context.Context, sessionId string) (int, error) {
	if sessionId == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM refresh_tokens WHERE session_id = $1", sessionId)
	if err != nil {
		return 0, fmt.Errorf("remove refresh tokens by session: %w", err)
	}
	n := int(tag.RowsAffected())
	s.logger.Debug("refresh tokens removed by session", "session_prefix", handlePrefix(sessionId), "count", n)
	return n, nil
}

// RemoveOnlineRefreshTokensBySession deletes every refresh token whose
// SessionId equals sessionId and whose AuthorizedScopes does not hold the
// exact element "offline_access", and returns how many rows were removed.
// Tokens whose scopes are absent, null or empty are online and removed; a
// scope that only contains the text (such as "offline_access_extended")
// does not make a token offline. Zero matches return (0, nil). An empty
// sessionId returns (0, nil) without issuing SQL, so records without a
// session are never selected.
//
// The session's rows are located on the refresh_tokens.session_id locator
// column and locked (SELECT ... FOR UPDATE) inside one transaction; each
// payload is decoded with the store codec and its scopes are compared
// element by element in Go; the online handles are then deleted in the
// same transaction. The lock keeps a concurrent UpdateRefreshToken from
// changing a token's scopes between the decision and the delete. The scopes
// are judged in Go rather than in SQL because PostgreSQL's jsonb rejects
// the \u0000 escape the codec may legitimately carry, so an SQL-side decode
// would fail the whole session on one such token. Any decode or database
// error rolls the transaction back: nothing is removed and the error is
// returned.
func (s *RefreshTokenStore) RemoveOnlineRefreshTokensBySession(ctx context.Context, sessionId string) (int, error) {
	if sessionId == "" {
		return 0, nil
	}
	n := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			"SELECT handle, payload FROM refresh_tokens WHERE session_id = $1 FOR UPDATE", sessionId)
		if err != nil {
			return fmt.Errorf("query refresh tokens of session: %w", err)
		}
		var online []string
		for rows.Next() {
			var handle string
			var payload []byte
			if err := rows.Scan(&handle, &payload); err != nil {
				rows.Close()
				return fmt.Errorf("scan refresh token row: %w", err)
			}
			token := new(models.RefreshToken)
			if err := s.codec.decode(payload, token); err != nil {
				rows.Close()
				return fmt.Errorf("decode refresh token payload: %w", err)
			}
			if !slices.Contains(token.AuthorizedScopes, offlineAccessScope) {
				online = append(online, handle)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate refresh tokens of session: %w", err)
		}
		if len(online) == 0 {
			return nil
		}
		tag, err := tx.Exec(ctx,
			"DELETE FROM refresh_tokens WHERE handle = ANY($1) AND session_id = $2", online, sessionId)
		if err != nil {
			return fmt.Errorf("delete online refresh tokens: %w", err)
		}
		n = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("remove online refresh tokens by session: %w", err)
	}
	s.logger.Debug("online refresh tokens removed by session", "session_prefix", handlePrefix(sessionId), "count", n)
	return n, nil
}

// offlineAccessScope is the scope whose exact presence marks a refresh
// token as offline. It is spelled out here, matching the port contract,
// rather than imported from the core module's root package.
const offlineAccessScope = "offline_access"

// RemoveExpired deletes tokens whose derived expiry precedes cutoff,
// strictly (a record with expires_at == cutoff is kept), and returns how
// many were removed. It reclaims space only; expiry judgement remains on
// the core side, so no clock function appears in the SQL.
func (s *RefreshTokenStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM refresh_tokens WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired refresh tokens: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *RefreshTokenStore) ConformsTo() string {
	return storagetest.SuiteVersion
}
