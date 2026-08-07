// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/gonewx/wicket/keymgmt"
	"github.com/gonewx/wicket/keymgmt/keymgmttest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyRecordStore is the pgx-backed adapter for the wicket keymgmt.RecordStore
// port. Records live in the key_records table with handle as the primary
// key; public_id, phase, and version are mirrored as real columns because
// they back the partial unique index and the optimistic-concurrency guard,
// while every remaining model field is serialized into the payload column.
// Key retirement is driven by the manager's core-side reconciliation, so
// this adapter carries no reclamation method — it is the only store without
// a cleanup entry point.
type KeyRecordStore struct {
	baseStore
}

// NewKeyRecordStore assembles a key record store on a host-owned pool. The
// pool is never created or closed here; a nil logger falls back to
// slog.Default() via newBase.
func NewKeyRecordStore(pool *pgxpool.Pool, logger *slog.Logger) *KeyRecordStore {
	return &KeyRecordStore{baseStore: newBase(pool, logger)}
}

// Get returns an independent copy of the stored record. The caller may
// mutate the returned value without affecting the store: every read decodes
// the payload afresh. A missing handle fails with keymgmt.ErrRecordNotFound;
// the record is never (nil, nil).
func (s *KeyRecordStore) Get(ctx context.Context, handle string) (*keymgmt.Record, error) {
	// The keymgmt suite requires a canceled read to surface the exact
	// missing sentinel. pgx fails a query under a canceled context with a
	// wrapped context.Canceled, which no error mapping can turn into
	// ErrRecordNotFound, so the check happens here.
	if err := ctx.Err(); err != nil {
		return nil, keymgmt.ErrRecordNotFound
	}
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM key_records WHERE handle = $1", handle).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, keymgmt.ErrRecordNotFound)
	}
	rec := new(keymgmt.Record)
	if err := s.codec.decode(payload, rec); err != nil {
		return nil, fmt.Errorf("decode key record payload: %w", err)
	}
	s.logger.Debug("key record read", "handle_prefix", handlePrefix(handle), "found", true)
	return rec, nil
}

// Create inserts a new record. The write is insert-only: a handle that
// already exists or a non-retired public id that already exists fails with
// keymgmt.ErrDuplicateKey (the partial unique index WHERE phase <> 'retired'
// lets retired records reuse a public id), and the stored record is left
// unchanged. The stored copy carries version 1 and the caller's record.Version
// is written back with the same value; the caller object is untouched on
// failure.
func (s *KeyRecordStore) Create(ctx context.Context, record *keymgmt.Record) error {
	enc := *record
	enc.Version = 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("create key record payload: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		"INSERT INTO key_records (handle, public_id, phase, version, payload) VALUES ($1, $2, $3, 1, $4)",
		record.Handle, record.PublicID, record.Phase, payload)
	if err != nil {
		return mapDuplicateErr(err, keymgmt.ErrDuplicateKey)
	}
	record.Version = 1
	s.logger.Debug("key record stored", "handle_prefix", handlePrefix(record.Handle))
	return nil
}

// Update replaces the stored record under an optimistic concurrency check
// expressed as a single conditional UPDATE: the row is modified only when
// the stored version equals expectedVersion, and the new stored copy carries
// expectedVersion+1, written back to the caller's record.Version. A zero-row
// outcome is re-checked for existence: a missing handle fails with
// keymgmt.ErrRecordNotFound, a version mismatch with
// keymgmt.ErrVersionConflict. Either failure leaves the stored record and
// the caller object untouched.
func (s *KeyRecordStore) Update(ctx context.Context, record *keymgmt.Record, expectedVersion uint64) error {
	// The version column is bigint: expectedVersion+1 must stay inside the
	// int64 range. Unreachable in practice; defensive (AD-3).
	if expectedVersion >= math.MaxInt64 {
		return fmt.Errorf("key record version %d out of range", expectedVersion)
	}
	enc := *record
	enc.Version = expectedVersion + 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("update key record payload: %w", err)
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE key_records SET version = version + 1, public_id = $2, phase = $3, payload = $4 WHERE handle = $1 AND version = $5",
		record.Handle, record.PublicID, record.Phase, payload, expectedVersion)
	if err != nil {
		return mapDuplicateErr(err, keymgmt.ErrDuplicateKey)
	}
	if tag.RowsAffected() == 1 {
		record.Version = expectedVersion + 1
		s.logger.Debug("key record updated", "handle_prefix", handlePrefix(record.Handle))
		return nil
	}
	var one int
	err = s.pool.QueryRow(ctx,
		"SELECT 1 FROM key_records WHERE handle = $1", record.Handle).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return keymgmt.ErrRecordNotFound
		}
		return fmt.Errorf("recheck key record existence: %w", err)
	}
	return keymgmt.ErrVersionConflict
}

// Delete removes the record under handle. Deleting a handle that is not
// present fails with keymgmt.ErrRecordNotFound — unlike every other store in
// this package, a missing delete is not a no-op: the keymgmt suite requires
// the exact sentinel.
func (s *KeyRecordStore) Delete(ctx context.Context, handle string) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM key_records WHERE handle = $1", handle)
	if err != nil {
		return fmt.Errorf("delete key record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return keymgmt.ErrRecordNotFound
	}
	s.logger.Debug("key record removed", "handle_prefix", handlePrefix(handle))
	return nil
}

// List returns an independent copy of every stored record, in an empty but
// non-nil slice when the store holds none. Each row is decoded afresh, so
// mutating a returned record never affects the store.
func (s *KeyRecordStore) List(ctx context.Context) ([]*keymgmt.Record, error) {
	rows, err := s.pool.Query(ctx, "SELECT payload FROM key_records")
	if err != nil {
		return nil, fmt.Errorf("query key records: %w", err)
	}
	defer rows.Close()

	records := []*keymgmt.Record{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan key record row: %w", err)
		}
		rec := new(keymgmt.Record)
		if err := s.codec.decode(payload, rec); err != nil {
			return nil, fmt.Errorf("decode key record payload: %w", err)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read key record rows: %w", err)
	}
	s.logger.Debug("key records listed", "count", len(records))
	return records, nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *KeyRecordStore) ConformsTo() string {
	return keymgmttest.SuiteVersion
}
