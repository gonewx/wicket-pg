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

// BackchannelAuthenticationRequestStore is the pgx-backed adapter for the
// wicket storage.BackchannelAuthenticationRequestStore port. Each record is
// one row keyed by the opaque auth_req_id (handle is the primary key) with
// the model serialized into the payload column. expires_at is derived at
// write time from the model's ExpirationTime — a zero value is stored as
// NULL so the record never expires — and reclaimed only by an explicit
// RemoveExpired call.
type BackchannelAuthenticationRequestStore struct {
	baseStore
}

// NewBackchannelAuthenticationRequestStore assembles a CIBA request store
// on a host-owned pool. The pool is never created or closed here; a nil
// logger falls back to slog.Default() via newBase.
func NewBackchannelAuthenticationRequestStore(pool *pgxpool.Pool, logger *slog.Logger) *BackchannelAuthenticationRequestStore {
	return &BackchannelAuthenticationRequestStore{baseStore: newBase(pool, logger)}
}

// StoreBackchannelAuthenticationRequest writes a record under the
// caller-supplied request id. The write is insert-only: a request id that
// already exists fails with storage.ErrDuplicateHandle and the stored
// record is left unchanged. expires_at is derived from the model's
// ExpirationTime: a zero value is stored as NULL (never expires), a
// non-zero value is stored as-is; no clock inside the adapter is involved.
func (s *BackchannelAuthenticationRequestStore) StoreBackchannelAuthenticationRequest(ctx context.Context, requestID string, data *models.BackchannelAuthenticationRequest) error {
	payload, err := s.codec.encode(data)
	if err != nil {
		return fmt.Errorf("store backchannel authentication request payload: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO backchannel_auth_requests (handle, expires_at, payload) VALUES ($1, $2, $3)",
		requestID, expiresAtPtr(data.ExpirationTime), payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	s.logger.Debug("backchannel authentication request stored", "handle_prefix", handlePrefix(requestID))
	return nil
}

// FindBackchannelAuthenticationRequest returns an independent copy of the
// record for the request id. A missing request id fails with
// storage.ErrNotFound and a nil record; the result is never (nil, nil).
func (s *BackchannelAuthenticationRequestStore) FindBackchannelAuthenticationRequest(ctx context.Context, requestID string) (*models.BackchannelAuthenticationRequest, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM backchannel_auth_requests WHERE handle = $1", requestID).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	data := new(models.BackchannelAuthenticationRequest)
	if err := s.codec.decode(payload, data); err != nil {
		return nil, fmt.Errorf("decode backchannel authentication request payload: %w", err)
	}
	s.logger.Debug("backchannel authentication request read", "handle_prefix", handlePrefix(requestID), "found", true)
	return data, nil
}

// UpdateBackchannelAuthenticationRequest replaces the payload and the
// derived expires_at for the record under the request id. The write is a
// state-migration replacement, not a version-guarded update; updating a
// request id that is not present succeeds as a no-op.
func (s *BackchannelAuthenticationRequestStore) UpdateBackchannelAuthenticationRequest(ctx context.Context, requestID string, data *models.BackchannelAuthenticationRequest) error {
	payload, err := s.codec.encode(data)
	if err != nil {
		return fmt.Errorf("update backchannel authentication request payload: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		"UPDATE backchannel_auth_requests SET payload = $2, expires_at = $3 WHERE handle = $1",
		requestID, payload, expiresAtPtr(data.ExpirationTime))
	if err != nil {
		return fmt.Errorf("update backchannel authentication request: %w", err)
	}
	s.logger.Debug("backchannel authentication request updated", "handle_prefix", handlePrefix(requestID))
	return nil
}

// RemoveBackchannelAuthenticationRequest deletes the record for the request
// id. Removing a request id that is not present is a successful no-op.
func (s *BackchannelAuthenticationRequestStore) RemoveBackchannelAuthenticationRequest(ctx context.Context, requestID string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM backchannel_auth_requests WHERE handle = $1", requestID)
	if err != nil {
		return fmt.Errorf("remove backchannel authentication request: %w", err)
	}
	s.logger.Debug("backchannel authentication request removed", "handle_prefix", handlePrefix(requestID))
	return nil
}

// RemoveExpired deletes records whose ExpirationTime precedes cutoff,
// strictly (a record with expires_at == cutoff is kept), and returns how
// many were removed. Rows with a NULL expires_at (zero-value ExpirationTime,
// never expires) are never touched. It reclaims space only; expiry
// judgement remains on the core side, so no clock function appears in the
// SQL.
func (s *BackchannelAuthenticationRequestStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM backchannel_auth_requests WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired backchannel authentication requests: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *BackchannelAuthenticationRequestStore) ConformsTo() string {
	return storagetest.SuiteVersion
}

// expiresAtPtr converts the ExpirationTime-family value for storage: a zero
// value becomes nil (SQL NULL, never expires) and a non-zero value is kept
// as-is. Passing a zero time.Time directly would be encoded by pgx as the
// non-NULL instant 0001-01-01T00:00:00Z, which RemoveExpired would then
// treat as long-expired.
func expiresAtPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
