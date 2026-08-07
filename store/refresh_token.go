// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// back with the same value; the caller object is untouched on failure.
func (s *RefreshTokenStore) StoreRefreshToken(ctx context.Context, handle string, token *models.RefreshToken) error {
	enc := *token
	enc.Version = 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("store refresh token payload: %w", err)
	}
	expiresAt := token.CreationTime.Add(time.Duration(token.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO refresh_tokens (handle, expires_at, version, payload) VALUES ($1, $2, 1, $3)",
		handle, expiresAt, payload)
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
		"UPDATE refresh_tokens SET version = version + 1, expires_at = $2, payload = $3 WHERE handle = $1 AND version = $4",
		handle, expiresAt, payload, expectedVersion)
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
