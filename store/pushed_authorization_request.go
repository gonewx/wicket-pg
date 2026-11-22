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
	"github.com/jackc/pgx/v5/pgxpool"
)

// PushedAuthorizationRequestStore is the pgx-backed adapter for the wicket
// storage.PushedAuthorizationRequestStore port. Records live in the
// pushed_authorization_requests table under the key the core derives with
// storage.PushedAuthorizationRequestKey; the store never sees the
// request_uri and never generates keys.
//
// The whole model is serialized into the payload column. client_id,
// creation_time, entry_expiration and retention_expiration are visible
// columns copied from the model at write time for operator queries; they are
// never read back, because timestamptz keeps only microseconds. Every read
// decodes the model from payload alone. retention_expiration drives
// reclamation through an explicit RemoveExpired call.
type PushedAuthorizationRequestStore struct {
	baseStore
}

// NewPushedAuthorizationRequestStore assembles a pushed authorization
// request store on a host-owned pool. The pool is never created or closed
// here; a nil logger falls back to slog.Default() via newBase.
func NewPushedAuthorizationRequestStore(pool *pgxpool.Pool, logger *slog.Logger) *PushedAuthorizationRequestStore {
	return &PushedAuthorizationRequestStore{baseStore: newBase(pool, logger)}
}

// StorePushedAuthorizationRequest writes a request under key. The write is
// insert-only: a key that already exists fails with
// storage.ErrDuplicateHandle and the stored record is left unchanged. The
// opaque Payload bytes travel base64-encoded inside the payload container,
// so NUL bytes and invalid UTF-8 are stored losslessly. An empty ClientId is
// written to the client_id column as NULL.
func (s *PushedAuthorizationRequestStore) StorePushedAuthorizationRequest(ctx context.Context, key string, request *models.PushedAuthorizationRequest) error {
	if request == nil {
		return errors.New("store pushed authorization request: nil request")
	}
	payload, err := s.codec.encode(request)
	if err != nil {
		return fmt.Errorf("store pushed authorization request payload: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO pushed_authorization_requests (key, client_id, creation_time, entry_expiration, retention_expiration, payload) VALUES ($1, $2, $3, $4, $5, $6)",
		key, nullIfEmpty(request.ClientId), request.CreationTime, request.EntryExpiration, request.RetentionExpiration, payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	s.logger.Debug("pushed authorization request stored", "handle_prefix", handlePrefix(key))
	return nil
}

// GetPushedAuthorizationRequest returns an independent copy of the request
// stored under key without consuming it. A missing key fails with
// storage.ErrNotFound; the record is never (nil, nil). Expiry is not
// judged.
func (s *PushedAuthorizationRequestStore) GetPushedAuthorizationRequest(ctx context.Context, key string) (*models.PushedAuthorizationRequest, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM pushed_authorization_requests WHERE key = $1", key).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	request := new(models.PushedAuthorizationRequest)
	if err := s.codec.decode(payload, request); err != nil {
		return nil, fmt.Errorf("decode pushed authorization request payload: %w", err)
	}
	s.logger.Debug("pushed authorization request read", "handle_prefix", handlePrefix(key), "found", true)
	return request, nil
}

// TakePushedAuthorizationRequest atomically removes the request stored under
// key and returns an independent copy of it, in a single DELETE ...
// RETURNING statement with no explicit transaction. A miss (absent or
// already taken) fails with storage.ErrNotFound, so of any number of
// concurrent takes of the same key exactly one succeeds. Expiry is not
// judged: an expired request is taken like any other. If the payload fails
// to decode after the delete has committed, the row is already removed, the
// caller gets a wrapped decode error (not storage.ErrNotFound), and the
// request is not restored. The same holds for a transport failure (context
// cancellation, lost connection) after the delete has committed but before
// the returned row is read: any error other than storage.ErrNotFound may
// mean the request was already consumed, and a retry then misses with
// storage.ErrNotFound.
func (s *PushedAuthorizationRequestStore) TakePushedAuthorizationRequest(ctx context.Context, key string) (*models.PushedAuthorizationRequest, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"DELETE FROM pushed_authorization_requests WHERE key = $1 RETURNING payload", key).Scan(&payload)
	if err != nil {
		return nil, fmt.Errorf("take pushed authorization request: %w", mapReadErr(err, storage.ErrNotFound))
	}
	request := new(models.PushedAuthorizationRequest)
	if err := s.codec.decode(payload, request); err != nil {
		return nil, fmt.Errorf("decode taken pushed authorization request payload: %w", err)
	}
	s.logger.Debug("pushed authorization request taken", "handle_prefix", handlePrefix(key))
	return request, nil
}

// RemoveExpired deletes requests whose RetentionExpiration precedes cutoff,
// strictly (a record with retention_expiration == cutoff is kept), and
// returns how many were removed. EntryExpiration plays no part: a request
// past its entry expiration but within its retention stays. A zero
// RetentionExpiration is stored as-is and reclaimed by any later cutoff.
// Expiry judgement remains on the core side, so no clock function appears
// in the SQL.
func (s *PushedAuthorizationRequestStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM pushed_authorization_requests WHERE retention_expiration < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired pushed authorization requests: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *PushedAuthorizationRequestStore) ConformsTo() string {
	return storagetest.SuiteVersion
}
