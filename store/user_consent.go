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

// UserConsentStore is the pgx-backed adapter for the wicket
// storage.UserConsentStore port. Records live in the user_consents table
// with the model serialized into the payload column; expires_at is derived
// from the consent's Expiration pointer at write time (a nil pointer stores
// NULL, meaning the record is never cleaned up) and reclaimed only by an
// explicit RemoveExpired call. The natural key (subject_id, client_id) makes
// the write an upsert: a repeated decision for the same pair replaces the
// earlier record, including every real column.
type UserConsentStore struct {
	baseStore
}

// NewUserConsentStore assembles a user consent store on a host-owned pool.
// The pool is never created or closed here; a nil logger falls back to
// slog.Default() via newBase.
func NewUserConsentStore(pool *pgxpool.Pool, logger *slog.Logger) *UserConsentStore {
	return &UserConsentStore{baseStore: newBase(pool, logger)}
}

// StoreUserConsent writes a consent under its (subjectId, clientId) natural
// key. The write is an upsert: a consent for a pair that already exists
// replaces the stored record, refreshing both expires_at and payload so no
// stale column survives. A nil Expiration stores NULL in expires_at, marking
// the record as never expiring.
func (s *UserConsentStore) StoreUserConsent(ctx context.Context, consent *models.Consent) error {
	payload, err := s.codec.encode(consent)
	if err != nil {
		return fmt.Errorf("store user consent payload: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO user_consents (subject_id, client_id, expires_at, payload) VALUES ($1, $2, $3, $4) "+
			"ON CONFLICT (subject_id, client_id) DO UPDATE SET expires_at = EXCLUDED.expires_at, payload = EXCLUDED.payload",
		consent.SubjectId, consent.ClientId, consent.Expiration, payload)
	if err != nil {
		return fmt.Errorf("store user consent: %w", err)
	}
	s.logger.Debug("user consent stored", "subject_prefix", handlePrefix(consent.SubjectId), "client_prefix", handlePrefix(consent.ClientId))
	return nil
}

// GetUserConsent returns an independent copy of the stored consent. The
// caller may mutate the returned value without affecting the store. A
// missing pair fails with storage.ErrNotFound; the record is never
// (nil, nil).
func (s *UserConsentStore) GetUserConsent(ctx context.Context, subjectId, clientId string) (*models.Consent, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM user_consents WHERE subject_id = $1 AND client_id = $2",
		subjectId, clientId).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	consent := new(models.Consent)
	if err := s.codec.decode(payload, consent); err != nil {
		return nil, fmt.Errorf("decode user consent payload: %w", err)
	}
	s.logger.Debug("user consent read", "subject_prefix", handlePrefix(subjectId), "client_prefix", handlePrefix(clientId), "found", true)
	return consent, nil
}

// GetAllUserConsents returns every consent stored for subjectId. An empty
// result is an empty, non-nil slice with a nil error.
func (s *UserConsentStore) GetAllUserConsents(ctx context.Context, subjectId string) ([]*models.Consent, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT payload FROM user_consents WHERE subject_id = $1", subjectId)
	if err != nil {
		return nil, fmt.Errorf("list user consents: %w", err)
	}
	defer rows.Close()

	out := make([]*models.Consent, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan user consent row: %w", err)
		}
		consent := new(models.Consent)
		if err := s.codec.decode(payload, consent); err != nil {
			return nil, fmt.Errorf("decode user consent payload: %w", err)
		}
		out = append(out, consent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list user consents: %w", err)
	}
	s.logger.Debug("user consents listed", "subject_prefix", handlePrefix(subjectId), "count", len(out))
	return out, nil
}

// RemoveUserConsent deletes the consent for the pair. Removing a pair that
// is not present is a successful no-op.
func (s *UserConsentStore) RemoveUserConsent(ctx context.Context, subjectId, clientId string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM user_consents WHERE subject_id = $1 AND client_id = $2",
		subjectId, clientId)
	if err != nil {
		return fmt.Errorf("remove user consent: %w", err)
	}
	s.logger.Debug("user consent removed", "subject_prefix", handlePrefix(subjectId), "client_prefix", handlePrefix(clientId))
	return nil
}

// RemoveExpired deletes consents whose stored expiry precedes cutoff,
// strictly (a record with expires_at == cutoff is kept), and returns how
// many were removed. Records with NULL expires_at (never expiring) are left
// untouched. It reclaims space only; expiry judgement remains on the core
// side, so no clock function appears in the SQL.
func (s *UserConsentStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM user_consents WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired user consents: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *UserConsentStore) ConformsTo() string {
	return storagetest.SuiteVersion
}
