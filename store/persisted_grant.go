// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/gonewx/wicket/storage/storagetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PersistedGrantStore is the pgx-backed adapter for the wicket
// storage.PersistedGrantStore port. Records live in the persisted_grants
// table with the model serialized into the payload column; the four filter
// dimensions (subject, session, client, type) and the derived expiry are
// real columns so batch revocation and filtering are indexed. The write is
// an upsert on the single-column key: a repeated store for the same key
// replaces the earlier record, refreshing every real column so no stale
// filter value survives.
type PersistedGrantStore struct {
	baseStore
}

// NewPersistedGrantStore assembles a persisted grant store on a host-owned
// pool. The pool is never created or closed here; a nil logger falls back
// to slog.Default() via newBase.
func NewPersistedGrantStore(pool *pgxpool.Pool, logger *slog.Logger) *PersistedGrantStore {
	return &PersistedGrantStore{baseStore: newBase(pool, logger)}
}

// Store writes a grant under its caller-chosen natural key. The write is
// an upsert: a grant whose key already exists replaces the stored record,
// refreshing every real column — the four filter dimensions, the derived
// expires_at, and the payload — so no stale column survives. A nil
// Expiration stores NULL in expires_at, marking the record as never
// expiring.
func (s *PersistedGrantStore) Store(ctx context.Context, grant *models.PersistedGrant) error {
	payload, err := s.codec.encode(grant)
	if err != nil {
		return fmt.Errorf("store persisted grant payload: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO persisted_grants (key, subject_id, session_id, client_id, type, expires_at, payload) "+
			"VALUES ($1, $2, $3, $4, $5, $6, $7) "+
			"ON CONFLICT (key) DO UPDATE SET subject_id = EXCLUDED.subject_id, session_id = EXCLUDED.session_id, "+
			"client_id = EXCLUDED.client_id, type = EXCLUDED.type, expires_at = EXCLUDED.expires_at, "+
			"payload = EXCLUDED.payload",
		grant.Key, grant.SubjectId, grant.SessionId, grant.ClientId, grant.Type, grant.Expiration, payload)
	if err != nil {
		return fmt.Errorf("store persisted grant: %w", err)
	}
	s.logger.Debug("persisted grant stored", "key_prefix", handlePrefix(grant.Key), "subject_prefix", handlePrefix(grant.SubjectId))
	return nil
}

// Get returns an independent copy of the stored grant. The caller may
// mutate the returned value without affecting the store. A missing key
// fails with storage.ErrNotFound; the record is never (nil, nil).
func (s *PersistedGrantStore) Get(ctx context.Context, key string) (*models.PersistedGrant, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM persisted_grants WHERE key = $1", key).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	grant := new(models.PersistedGrant)
	if err := s.codec.decode(payload, grant); err != nil {
		return nil, fmt.Errorf("decode persisted grant payload: %w", err)
	}
	s.logger.Debug("persisted grant read", "key_prefix", handlePrefix(key), "found", true)
	return grant, nil
}

// GetAll returns every grant matching filter, with each condition that is
// set ANDed together (a zero-length multi-value slice does not filter).
// An empty result is an empty, non-nil slice with a nil error.
func (s *PersistedGrantStore) GetAll(ctx context.Context, filter *storage.PersistedGrantFilter) ([]*models.PersistedGrant, error) {
	where, args := s.buildFilter(filter)
	query := "SELECT payload FROM persisted_grants"
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list persisted grants: %w", err)
	}
	defer rows.Close()

	out := make([]*models.PersistedGrant, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan persisted grant row: %w", err)
		}
		grant := new(models.PersistedGrant)
		if err := s.codec.decode(payload, grant); err != nil {
			return nil, fmt.Errorf("decode persisted grant payload: %w", err)
		}
		out = append(out, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list persisted grants: %w", err)
	}
	s.logger.Debug("persisted grants listed", "count", len(out))
	return out, nil
}

// Remove deletes the grant under key. Removing a key that is not present
// is a successful no-op.
func (s *PersistedGrantStore) Remove(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM persisted_grants WHERE key = $1", key)
	if err != nil {
		return fmt.Errorf("remove persisted grant: %w", err)
	}
	s.logger.Debug("persisted grant removed", "key_prefix", handlePrefix(key))
	return nil
}

// RemoveAll deletes every grant matching filter, with the same condition
// semantics as GetAll — both share buildFilter so a batch revocation and
// the listing that precedes it cannot drift apart. A filter matching
// nothing is a successful no-op.
func (s *PersistedGrantStore) RemoveAll(ctx context.Context, filter *storage.PersistedGrantFilter) error {
	where, args := s.buildFilter(filter)
	query := "DELETE FROM persisted_grants"
	if where != "" {
		query += " WHERE " + where
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("remove persisted grants: %w", err)
	}
	s.logger.Debug("persisted grants removed", "count", tag.RowsAffected())
	return nil
}

// RemoveExpired deletes grants whose stored expiry precedes cutoff,
// strictly (a record with expires_at == cutoff is kept), and returns how
// many were removed. Records with NULL expires_at (never expiring) are
// left untouched. It reclaims space only; expiry judgement remains on the
// core side, so no clock function appears in the SQL.
func (s *PersistedGrantStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM persisted_grants WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired persisted grants: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *PersistedGrantStore) ConformsTo() string {
	return storagetest.SuiteVersion
}

// buildFilter renders the WHERE clause and arguments for the persisted
// grant filter. Every condition that is set participates, ANDed together:
// a single value is an equality on its column, a non-empty multi-value
// list is a column = ANY($n) with the slice bound natively. A nil or
// all-empty filter yields no conditions, matching every grant.
func (s *PersistedGrantStore) buildFilter(filter *storage.PersistedGrantFilter) (string, []any) {
	if filter == nil {
		return "", nil
	}
	conditions := make([]string, 0, 6)
	args := make([]any, 0, 6)
	add := func(cond string, arg any) {
		conditions = append(conditions, fmt.Sprintf("%s = $%d", cond, len(args)+1))
		args = append(args, arg)
	}
	if filter.SubjectId != "" {
		add("subject_id", filter.SubjectId)
	}
	if filter.SessionId != "" {
		add("session_id", filter.SessionId)
	}
	if filter.ClientId != "" {
		add("client_id", filter.ClientId)
	}
	if len(filter.ClientIds) > 0 {
		conditions = append(conditions, fmt.Sprintf("client_id = ANY($%d)", len(args)+1))
		args = append(args, filter.ClientIds)
	}
	if filter.Type != "" {
		add("type", filter.Type)
	}
	if len(filter.Types) > 0 {
		conditions = append(conditions, fmt.Sprintf("type = ANY($%d)", len(args)+1))
		args = append(args, filter.Types)
	}
	return strings.Join(conditions, " AND "), args
}
