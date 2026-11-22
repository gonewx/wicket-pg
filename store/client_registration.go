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

// ClientRegistrationStore is the pgx-backed adapter for the wicket
// storage.ClientRegistrationStore port, and on the same value for the
// storage.ClientOriginLookup port: both read the client_registrations
// table, so CORS decisions see exactly the registered clients every other
// read path sees.
//
// One row holds one client id for its whole life. A live row carries the
// payload (the encoded client, the registration access token digest and the
// registration time); a removal turns the row into a tombstone in place,
// keeping only client_id, revision and registration_time, so a removed
// client id can never be registered again and no secret hash or digest
// survives the removal.
//
// The revision column is the only source of a record's revision. The
// digest, registration time and allowed origins columns are copies written
// for operator queries and the origin lookup index; reads never rebuild the
// record from them. Registered clients have no server-side lifecycle, so the
// store carries no reclamation method.
type ClientRegistrationStore struct {
	baseStore
}

// NewClientRegistrationStore assembles a client registration store on a
// host-owned pool. The pool is never created or closed here; a nil logger
// falls back to slog.Default() via newBase.
func NewClientRegistrationStore(pool *pgxpool.Pool, logger *slog.Logger) *ClientRegistrationStore {
	return &ClientRegistrationStore{baseStore: newBase(pool, logger)}
}

// clientRegistrationPayload is the model written into the payload
// container. Client holds the bytes of storage.EncodeClient as they are; the
// revision is deliberately absent, because it lives in the revision column
// alone.
type clientRegistrationPayload struct {
	Client                        []byte    `json:"client"`
	RegistrationAccessTokenDigest string    `json:"registrationAccessTokenDigest"`
	RegistrationTime              time.Time `json:"registrationTime"`
}

// clientRegistrationWrite is everything a register or update writes: the
// client id, the payload container and the visible column copies.
type clientRegistrationWrite struct {
	clientId string
	payload  []byte
	digest   string
	time     time.Time
	origins  []string
}

// prepareClientRegistration validates a registration and encodes it for a
// write. A nil registration, a nil client and an empty client id are
// rejected before any statement is sent. Nil allowed origins become an empty
// array, because pgx writes a nil slice as NULL and the column is NOT NULL.
func (s *ClientRegistrationStore) prepareClientRegistration(op string, registration *models.ClientRegistration) (*clientRegistrationWrite, error) {
	if registration == nil {
		return nil, fmt.Errorf("%s: nil registration", op)
	}
	if registration.Client == nil {
		return nil, fmt.Errorf("%s: registration carries no client", op)
	}
	if registration.Client.ClientId == "" {
		return nil, fmt.Errorf("%s: registration carries no client id", op)
	}
	client, err := storage.EncodeClient(registration.Client)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	payload, err := s.codec.encode(&clientRegistrationPayload{
		Client:                        client,
		RegistrationAccessTokenDigest: registration.RegistrationAccessTokenDigest,
		RegistrationTime:              registration.RegistrationTime,
	})
	if err != nil {
		return nil, fmt.Errorf("%s payload: %w", op, err)
	}
	origins := registration.Client.AllowedCorsOrigins
	if origins == nil {
		origins = []string{}
	}
	return &clientRegistrationWrite{
		clientId: registration.Client.ClientId,
		payload:  payload,
		digest:   registration.RegistrationAccessTokenDigest,
		time:     registration.RegistrationTime,
		origins:  origins,
	}, nil
}

// decodeClientRegistration restores a registration from its payload
// container and the revision column.
func (s *ClientRegistrationStore) decodeClientRegistration(payload []byte, revision int64) (*models.ClientRegistration, error) {
	var stored clientRegistrationPayload
	if err := s.codec.decode(payload, &stored); err != nil {
		return nil, fmt.Errorf("decode client registration payload: %w", err)
	}
	client, err := storage.DecodeClient(stored.Client)
	if err != nil {
		return nil, fmt.Errorf("decode client registration payload: %w", err)
	}
	return &models.ClientRegistration{
		Client:                        client,
		Revision:                      int(revision),
		RegistrationAccessTokenDigest: stored.RegistrationAccessTokenDigest,
		RegistrationTime:              stored.RegistrationTime,
	}, nil
}

// RegisterClient stores a new registration and returns its revision, 1. The
// Revision passed in is ignored. The write is a plain INSERT: the primary
// key rejects a client id that is registered or tombstoned with
// storage.ErrDuplicateHandle, leaving the stored row unchanged.
func (s *ClientRegistrationStore) RegisterClient(ctx context.Context, registration *models.ClientRegistration) (int, error) {
	w, err := s.prepareClientRegistration("register client", registration)
	if err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO client_registrations (client_id, revision, removed, registration_access_token_digest, registration_time, allowed_cors_origins, payload) "+
			"VALUES ($1, 1, false, $2, $3, $4, $5)",
		w.clientId, w.digest, w.time, w.origins, w.payload)
	if err != nil {
		return 0, mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	s.logger.Debug("client registration stored", "handle_prefix", handlePrefix(w.clientId))
	return 1, nil
}

// FindClientRegistration returns an independent copy of the registration of
// clientId. A missing or removed client id fails with storage.ErrNotFound;
// the record is never (nil, nil). A payload that fails to decode is a
// wrapped decode error, not storage.ErrNotFound.
func (s *ClientRegistrationStore) FindClientRegistration(ctx context.Context, clientId string) (*models.ClientRegistration, error) {
	var (
		payload  []byte
		revision int64
	)
	err := s.pool.QueryRow(ctx,
		"SELECT payload, revision FROM client_registrations WHERE client_id = $1 AND NOT removed",
		clientId).Scan(&payload, &revision)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	registration, err := s.decodeClientRegistration(payload, revision)
	if err != nil {
		return nil, err
	}
	s.logger.Debug("client registration read", "handle_prefix", handlePrefix(clientId), "found", true)
	return registration, nil
}

// FindClientById returns an independent copy of the registered client of
// clientId, read from the same row as FindClientRegistration. A missing or
// removed client id fails with storage.ErrNotFound.
func (s *ClientRegistrationStore) FindClientById(ctx context.Context, clientId string) (*models.Client, error) {
	registration, err := s.FindClientRegistration(ctx, clientId)
	if err != nil {
		return nil, err
	}
	return registration.Client, nil
}

// UpdateClientRegistration replaces the registration of
// registration.Client.ClientId under an optimistic concurrency check
// expressed as a single conditional UPDATE: the row changes only when it is
// live and its revision equals expectedRevision, and the same statement
// bumps the revision and refreshes the payload and the visible columns. It
// returns the committed revision and ignores the Revision passed in. A
// zero-row outcome is re-checked: a missing or removed client id fails with
// storage.ErrNotFound, a revision mismatch with storage.ErrVersionConflict.
// Either failure leaves the stored row unchanged.
func (s *ClientRegistrationStore) UpdateClientRegistration(ctx context.Context, registration *models.ClientRegistration, expectedRevision int) (int, error) {
	w, err := s.prepareClientRegistration("update client registration", registration)
	if err != nil {
		return 0, err
	}
	var revision int64
	err = s.pool.QueryRow(ctx,
		"UPDATE client_registrations SET revision = revision + 1, payload = $3, registration_access_token_digest = $4, "+
			"registration_time = $5, allowed_cors_origins = $6 "+
			"WHERE client_id = $1 AND NOT removed AND revision = $2 RETURNING revision",
		w.clientId, expectedRevision, w.payload, w.digest, w.time, w.origins).Scan(&revision)
	if err == nil {
		s.logger.Debug("client registration updated", "handle_prefix", handlePrefix(w.clientId))
		return int(revision), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("update client registration: %w", err)
	}
	return 0, s.missOrConflict(ctx, w.clientId)
}

// RemoveClientRegistration turns the registration of clientId into a
// tombstone when it is live and its revision equals expectedRevision, in a
// single conditional UPDATE that clears the payload and the digest and
// empties the allowed origins. A zero-row outcome is re-checked: a missing
// or already removed client id fails with storage.ErrNotFound, a revision
// mismatch with storage.ErrVersionConflict.
func (s *ClientRegistrationStore) RemoveClientRegistration(ctx context.Context, clientId string, expectedRevision int) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE client_registrations SET removed = true, payload = NULL, registration_access_token_digest = NULL, "+
			"allowed_cors_origins = '{}' "+
			"WHERE client_id = $1 AND NOT removed AND revision = $2",
		clientId, expectedRevision)
	if err != nil {
		return fmt.Errorf("remove client registration: %w", err)
	}
	if tag.RowsAffected() == 1 {
		s.logger.Debug("client registration removed", "handle_prefix", handlePrefix(clientId))
		return nil
	}
	return s.missOrConflict(ctx, clientId)
}

// missOrConflict classifies a conditional write that touched no row: no row
// or a tombstone is storage.ErrNotFound, a live row is
// storage.ErrVersionConflict.
func (s *ClientRegistrationStore) missOrConflict(ctx context.Context, clientId string) error {
	var removed bool
	err := s.pool.QueryRow(ctx,
		"SELECT removed FROM client_registrations WHERE client_id = $1", clientId).Scan(&removed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("recheck client registration existence: %w", err)
	}
	if removed {
		return storage.ErrNotFound
	}
	return storage.ErrVersionConflict
}

// FindClientsByAllowedOrigin returns independent copies of the registered
// clients whose AllowedCorsOrigins contain origin exactly (text equality:
// case, port and trailing slash all count). Removed clients never match;
// Enabled is not filtered. A miss returns an empty, non-nil slice. A payload
// that fails to decode is a wrapped decode error.
func (s *ClientRegistrationStore) FindClientsByAllowedOrigin(ctx context.Context, origin string) ([]*models.Client, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT payload, revision FROM client_registrations WHERE allowed_cors_origins @> ARRAY[$1]::text[] AND NOT removed",
		origin)
	if err != nil {
		return nil, fmt.Errorf("find clients by allowed origin: %w", err)
	}
	defer rows.Close()
	clients := []*models.Client{}
	for rows.Next() {
		var (
			payload  []byte
			revision int64
		)
		if err := rows.Scan(&payload, &revision); err != nil {
			return nil, fmt.Errorf("scan client registration: %w", err)
		}
		registration, err := s.decodeClientRegistration(payload, revision)
		if err != nil {
			return nil, err
		}
		clients = append(clients, registration.Client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find clients by allowed origin: %w", err)
	}
	s.logger.Debug("clients by allowed origin read", "count", len(clients))
	return clients, nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *ClientRegistrationStore) ConformsTo() string {
	return storagetest.SuiteVersion
}
