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
// storage.ErrDuplicateHandle and the stored record is left unchanged.
func (s *AuthorizationCodeStore) StoreAuthorizationCode(ctx context.Context, handle string, code *models.AuthorizationCode) error {
	payload, err := s.codec.encode(code)
	if err != nil {
		return fmt.Errorf("store authorization code payload: %w", err)
	}
	expiresAt := code.CreationTime.Add(time.Duration(code.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO authorization_codes (handle, expires_at, payload) VALUES ($1, $2, $3)",
		handle, expiresAt, payload)
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
