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

// ReferenceTokenStore is the pgx-backed adapter for the wicket
// storage.ReferenceTokenStore port. Records live in the reference_tokens
// table with the model serialized into the payload column; expires_at is
// derived at write time and reclaimed only by an explicit RemoveExpired
// call. Batch revocation by subject/client pair scans the payload column,
// because the schema carries no subject/client real columns.
type ReferenceTokenStore struct {
	baseStore
}

// NewReferenceTokenStore assembles a reference token store on a host-owned
// pool. The pool is never created or closed here; a nil logger falls back
// to slog.Default() via newBase.
func NewReferenceTokenStore(pool *pgxpool.Pool, logger *slog.Logger) *ReferenceTokenStore {
	return &ReferenceTokenStore{baseStore: newBase(pool, logger)}
}

// StoreReferenceToken writes a token under the caller-supplied handle. The
// write is insert-only: a handle that already exists fails with
// storage.ErrDuplicateHandle and the stored record is left unchanged.
func (s *ReferenceTokenStore) StoreReferenceToken(ctx context.Context, handle string, token *models.Token) error {
	payload, err := s.codec.encode(token)
	if err != nil {
		return fmt.Errorf("store reference token payload: %w", err)
	}
	expiresAt := token.CreationTime.Add(time.Duration(token.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO reference_tokens (handle, expires_at, payload) VALUES ($1, $2, $3)",
		handle, expiresAt, payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	s.logger.Debug("reference token stored", "handle_prefix", handlePrefix(handle))
	return nil
}

// GetReferenceToken returns an independent copy of the stored token. The
// caller may mutate the returned value without affecting the store. A
// missing handle fails with storage.ErrNotFound; the record is never
// (nil, nil).
func (s *ReferenceTokenStore) GetReferenceToken(ctx context.Context, handle string) (*models.Token, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM reference_tokens WHERE handle = $1", handle).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	token := new(models.Token)
	if err := s.codec.decode(payload, token); err != nil {
		return nil, fmt.Errorf("decode reference token payload: %w", err)
	}
	s.logger.Debug("reference token read", "handle_prefix", handlePrefix(handle), "found", true)
	return token, nil
}

// RemoveReferenceToken deletes the token under handle. Removing a handle
// that is not present is a successful no-op.
func (s *ReferenceTokenStore) RemoveReferenceToken(ctx context.Context, handle string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM reference_tokens WHERE handle = $1", handle)
	if err != nil {
		return fmt.Errorf("remove reference token: %w", err)
	}
	s.logger.Debug("reference token removed", "handle_prefix", handlePrefix(handle))
	return nil
}

// RemoveReferenceTokens revokes every token whose derived subject and
// client id match the pair. Because the table carries no subject/client
// columns, matching scans the payload column and decodes each row in Go;
// matched handles are then deleted in a single statement. An empty match
// set deletes nothing and succeeds.
func (s *ReferenceTokenStore) RemoveReferenceTokens(ctx context.Context, subjectId, clientId string) error {
	rows, err := s.pool.Query(ctx,
		"SELECT handle, payload FROM reference_tokens")
	if err != nil {
		return fmt.Errorf("scan reference tokens for revocation: %w", err)
	}
	defer rows.Close()

	var handles []string
	for rows.Next() {
		var handle string
		var payload []byte
		if err := rows.Scan(&handle, &payload); err != nil {
			return fmt.Errorf("scan reference token row: %w", err)
		}
		token := new(models.Token)
		if err := s.codec.decode(payload, token); err != nil {
			return fmt.Errorf("decode reference token payload: %w", err)
		}
		if token.SubjectId() == subjectId && token.ClientId == clientId {
			handles = append(handles, handle)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan reference tokens for revocation: %w", err)
	}
	if len(handles) == 0 {
		return nil
	}
	_, err = s.pool.Exec(ctx,
		"DELETE FROM reference_tokens WHERE handle = ANY($1)", handles)
	if err != nil {
		return fmt.Errorf("revoke reference tokens: %w", err)
	}
	s.logger.Debug("reference tokens revoked", "count", len(handles))
	return nil
}

// RemoveExpired deletes tokens whose derived expiry precedes cutoff,
// strictly (a record with expires_at == cutoff is kept), and returns how
// many were removed. It reclaims space only; expiry judgement remains on
// the core side, so no clock function appears in the SQL.
func (s *ReferenceTokenStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM reference_tokens WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired reference tokens: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *ReferenceTokenStore) ConformsTo() string {
	return storagetest.SuiteVersion
}
