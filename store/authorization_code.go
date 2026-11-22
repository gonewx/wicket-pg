// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/gonewx/wicket/storage/storagetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorizationCodeStore is the pgx-backed adapter for the wicket
// storage.AuthorizationCodeStore port. Records live in the
// authorization_codes table with the model serialized into the payload
// column; expires_at is derived at write time and reclaimed only by an
// explicit RemoveExpired call.
type AuthorizationCodeStore struct {
	baseStore
}

// NewAuthorizationCodeStore assembles an authorization code store on a
// host-owned pool. The pool is never created or closed here; a nil logger
// falls back to slog.Default() via newBase.
func NewAuthorizationCodeStore(pool *pgxpool.Pool, logger *slog.Logger) *AuthorizationCodeStore {
	return &AuthorizationCodeStore{baseStore: newBase(pool, logger)}
}

// StoreAuthorizationCode writes a code under the caller-supplied handle.
// The write is insert-only: a handle that already exists fails with
// storage.ErrDuplicateHandle and the stored record is left unchanged. The
// code's client and session are written to the client_id / session_id
// locator columns (empty values as NULL).
func (s *AuthorizationCodeStore) StoreAuthorizationCode(ctx context.Context, handle string, code *models.AuthorizationCode) error {
	payload, err := s.codec.encode(code)
	if err != nil {
		return fmt.Errorf("store authorization code payload: %w", err)
	}
	expiresAt := code.CreationTime.Add(time.Duration(code.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO authorization_codes (handle, client_id, session_id, expires_at, payload) VALUES ($1, $2, $3, $4, $5)",
		handle, nullIfEmpty(code.ClientId), nullIfEmpty(code.SessionId), expiresAt, payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	s.logger.Debug("authorization code stored", "handle_prefix", handlePrefix(handle))
	return nil
}

// GetAuthorizationCode returns an independent copy of the stored code. The
// caller may mutate the returned value without affecting the store. A
// missing handle fails with storage.ErrNotFound; the record is never
// (nil, nil).
func (s *AuthorizationCodeStore) GetAuthorizationCode(ctx context.Context, handle string) (*models.AuthorizationCode, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM authorization_codes WHERE handle = $1", handle).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	code := new(models.AuthorizationCode)
	if err := s.codec.decode(payload, code); err != nil {
		return nil, fmt.Errorf("decode authorization code payload: %w", err)
	}
	s.logger.Debug("authorization code read", "handle_prefix", handlePrefix(handle), "found", true)
	return code, nil
}

// RemoveAuthorizationCode deletes the code under handle. Removing a handle
// that is not present is a successful no-op.
func (s *AuthorizationCodeStore) RemoveAuthorizationCode(ctx context.Context, handle string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM authorization_codes WHERE handle = $1", handle)
	if err != nil {
		return fmt.Errorf("remove authorization code: %w", err)
	}
	s.logger.Debug("authorization code removed", "handle_prefix", handlePrefix(handle))
	return nil
}

// TakeAuthorizationCode atomically removes the code stored under handle and
// returns an independent copy of it, in a single DELETE ... RETURNING
// statement with no explicit transaction. A miss (absent or already taken)
// fails with storage.ErrNotFound, so of any number of concurrent takes of the
// same handle exactly one succeeds. Expiry is not judged: an expired code is
// taken like any other. If the payload fails to decode after the delete has
// committed, the row is already removed, the caller gets a wrapped decode
// error (not storage.ErrNotFound), and the code is not restored: a
// single-use code is consumed on take. The same holds for a transport
// failure (context cancellation, lost connection) after the delete has
// committed but before the returned row is read: any error other than
// storage.ErrNotFound may mean the code was already consumed, and a retry
// then misses with storage.ErrNotFound.
func (s *AuthorizationCodeStore) TakeAuthorizationCode(ctx context.Context, handle string) (*models.AuthorizationCode, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"DELETE FROM authorization_codes WHERE handle = $1 RETURNING payload", handle).Scan(&payload)
	if err != nil {
		return nil, fmt.Errorf("take authorization code: %w", mapReadErr(err, storage.ErrNotFound))
	}
	code := new(models.AuthorizationCode)
	if err := s.codec.decode(payload, code); err != nil {
		return nil, fmt.Errorf("decode taken authorization code payload: %w", err)
	}
	s.logger.Debug("authorization code taken", "handle_prefix", handlePrefix(handle))
	return code, nil
}

// RemoveAuthorizationCodesByClient deletes every authorization code whose
// ClientId equals clientId in one DELETE on the authorization_codes.client_id
// locator column and returns how many rows were removed. Zero matches return
// (0, nil). An empty clientId returns (0, nil) without issuing SQL, so
// records without a client are never selected.
func (s *AuthorizationCodeStore) RemoveAuthorizationCodesByClient(ctx context.Context, clientId string) (int, error) {
	if clientId == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM authorization_codes WHERE client_id = $1", clientId)
	if err != nil {
		return 0, fmt.Errorf("remove authorization codes by client: %w", err)
	}
	n := int(tag.RowsAffected())
	s.logger.Debug("authorization codes removed by client", "client_prefix", handlePrefix(clientId), "count", n)
	return n, nil
}

// RemoveAuthorizationCodesBySession deletes every authorization code whose
// SessionId equals sessionId in one DELETE on the
// authorization_codes.session_id locator column and returns how many rows
// were removed. Zero matches return (0, nil). An empty sessionId returns (0,
// nil) without issuing SQL, so records without a session are never selected.
func (s *AuthorizationCodeStore) RemoveAuthorizationCodesBySession(ctx context.Context, sessionId string) (int, error) {
	if sessionId == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM authorization_codes WHERE session_id = $1", sessionId)
	if err != nil {
		return 0, fmt.Errorf("remove authorization codes by session: %w", err)
	}
	n := int(tag.RowsAffected())
	s.logger.Debug("authorization codes removed by session", "session_prefix", handlePrefix(sessionId), "count", n)
	return n, nil
}

// RemoveExpired deletes codes whose derived expiry precedes cutoff, strictly
// (a record with expires_at == cutoff is kept), and returns how many were
// removed. It reclaims space only; expiry judgement remains on the core
// side, so no clock function appears in the SQL.
func (s *AuthorizationCodeStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM authorization_codes WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired authorization codes: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *AuthorizationCodeStore) ConformsTo() string {
	return storagetest.SuiteVersion
}

// handlePrefix caps a handle at eight characters for log records; full
// handle values and tokens must never appear in any log line.
func handlePrefix(handle string) string {
	if len(handle) > 8 {
		return handle[:8]
	}
	return handle
}
