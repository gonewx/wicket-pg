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

// DeviceFlowStore is the pgx-backed adapter for the wicket
// storage.DeviceFlowStore port. Each record carries both codes in one row:
// handle (= device code) is the primary key and user_code is the second
// unique lookup key, so one row backs both read paths and one deletion
// clears both codes. The model is serialized into the payload column;
// version is the optimistic concurrency column and the only source of the
// record's Version on read; expires_at is derived at write time and
// reclaimed only by an explicit RemoveExpired call.
type DeviceFlowStore struct {
	baseStore
}

// NewDeviceFlowStore assembles a device flow store on a host-owned pool.
// The pool is never created or closed here; a nil logger falls back to
// slog.Default() via newBase.
func NewDeviceFlowStore(pool *pgxpool.Pool, logger *slog.Logger) *DeviceFlowStore {
	return &DeviceFlowStore{baseStore: newBase(pool, logger)}
}

// StoreDeviceAuthorization writes a record under the caller-supplied device
// and user codes. The write is insert-only: either code already being
// present fails with storage.ErrDuplicateHandle and the stored record is
// left unchanged. The stored record carries version 1, written back to the
// caller's data.Version on success. expires_at is derived from the model's CreationTime and
// Lifetime, never from a clock inside the adapter; the record's session is
// written to the session_id locator column (empty as NULL).
func (s *DeviceFlowStore) StoreDeviceAuthorization(ctx context.Context, deviceCode string, userCode string, data *models.DeviceCode) error {
	enc := *data
	enc.Version = 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("store device authorization payload: %w", err)
	}
	expiresAt := data.CreationTime.Add(time.Duration(data.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO device_codes (handle, user_code, session_id, version, expires_at, payload) VALUES ($1, $2, $3, 1, $4, $5)",
		deviceCode, userCode, nullIfEmpty(data.SessionId), expiresAt, payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	data.Version = 1
	s.logger.Debug("device authorization stored",
		"device_code_prefix", handlePrefix(deviceCode),
		"user_code_prefix", handlePrefix(userCode))
	return nil
}

// FindByDeviceCode returns an independent copy of the record for the device
// code. A missing code fails with storage.ErrNotFound and a nil record; the
// result is never (nil, nil).
func (s *DeviceFlowStore) FindByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceCode, error) {
	data, err := s.scanRecord(s.pool.QueryRow(ctx,
		"SELECT version, payload FROM device_codes WHERE handle = $1", deviceCode))
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	s.logger.Debug("device authorization read", "device_code_prefix", handlePrefix(deviceCode), "found", true)
	return data, nil
}

// FindByUserCode returns an independent copy of the record for the user
// code. A missing code fails with storage.ErrNotFound and a nil record; the
// result is never (nil, nil).
func (s *DeviceFlowStore) FindByUserCode(ctx context.Context, userCode string) (*models.DeviceCode, error) {
	data, err := s.scanRecord(s.pool.QueryRow(ctx,
		"SELECT version, payload FROM device_codes WHERE user_code = $1", userCode))
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	s.logger.Debug("device authorization read", "user_code_prefix", handlePrefix(userCode), "found", true)
	return data, nil
}

// UpdateByUserCode replaces the record under user code only when the
// stored version equals expectedVersion, in one conditional UPDATE that
// also bumps the version column and refreshes the payload, the derived
// expires_at and the session_id locator column (the approval writes the
// session here, so the column must follow it). On success the new version
// expectedVersion+1 is written back to data.Version. Zero matched rows are
// told apart by a follow-up existence check: a missing user code fails with
// storage.ErrNotFound and creates nothing, a version mismatch fails with
// storage.ErrVersionConflict and leaves the record unchanged.
func (s *DeviceFlowStore) UpdateByUserCode(ctx context.Context, userCode string, data *models.DeviceCode, expectedVersion int) error {
	enc := *data
	enc.Version = expectedVersion + 1
	payload, err := s.codec.encode(&enc)
	if err != nil {
		return fmt.Errorf("update device authorization payload: %w", err)
	}
	expiresAt := data.CreationTime.Add(time.Duration(data.Lifetime) * time.Second)
	tag, err := s.pool.Exec(ctx,
		"UPDATE device_codes SET version = version + 1, payload = $2, expires_at = $3, session_id = $4 "+
			"WHERE user_code = $1 AND version = $5",
		userCode, payload, expiresAt, nullIfEmpty(data.SessionId), expectedVersion)
	if err != nil {
		return fmt.Errorf("update device authorization: %w", err)
	}
	if tag.RowsAffected() == 1 {
		data.Version = expectedVersion + 1
		s.logger.Debug("device authorization updated", "user_code_prefix", handlePrefix(userCode))
		return nil
	}
	var one int
	err = s.pool.QueryRow(ctx,
		"SELECT 1 FROM device_codes WHERE user_code = $1", userCode).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("recheck device authorization existence: %w", err)
	}
	return storage.ErrVersionConflict
}

// TakeByDeviceCode atomically removes the row stored under the device code
// — which carries its user code too — and returns the removed record, in
// one DELETE ... RETURNING. Of concurrent takes of the same device code
// exactly one gets the record; the others, and any take of a missing code,
// fail with (nil, storage.ErrNotFound). Expiry is not judged.
func (s *DeviceFlowStore) TakeByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceCode, error) {
	data, err := s.scanRecord(s.pool.QueryRow(ctx,
		"DELETE FROM device_codes WHERE handle = $1 RETURNING version, payload", deviceCode))
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	s.logger.Debug("device authorization taken", "device_code_prefix", handlePrefix(deviceCode))
	return data, nil
}

// RemoveByDeviceCode deletes the row carrying both codes; afterwards both
// the device code and the user code are gone. Removing a device code that
// is not present is a successful no-op.
func (s *DeviceFlowStore) RemoveByDeviceCode(ctx context.Context, deviceCode string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM device_codes WHERE handle = $1", deviceCode)
	if err != nil {
		return fmt.Errorf("remove device authorization: %w", err)
	}
	s.logger.Debug("device authorization removed", "device_code_prefix", handlePrefix(deviceCode))
	return nil
}

// RemoveDeviceCodesBySession deletes every device code whose SessionId equals
// sessionId in one DELETE on the device_codes.session_id locator column and
// returns how many rows were removed. Zero matches return (0, nil). An empty
// sessionId returns (0, nil) without issuing SQL, so records without a
// session are never selected. One row carries both the device code and its
// user code, so each removed authorization record counts once and misses
// afterwards through both FindByDeviceCode and FindByUserCode.
func (s *DeviceFlowStore) RemoveDeviceCodesBySession(ctx context.Context, sessionId string) (int, error) {
	if sessionId == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM device_codes WHERE session_id = $1", sessionId)
	if err != nil {
		return 0, fmt.Errorf("remove device codes by session: %w", err)
	}
	n := int(tag.RowsAffected())
	s.logger.Debug("device codes removed by session", "session_prefix", handlePrefix(sessionId), "count", n)
	return n, nil
}

// RemoveExpired deletes records whose derived expiry precedes cutoff,
// strictly (a record with expires_at == cutoff is kept), and returns how
// many were removed. It reclaims space only; expiry judgement remains on
// the core side, so no clock function appears in the SQL.
func (s *DeviceFlowStore) RemoveExpired(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM device_codes WHERE expires_at IS NOT NULL AND expires_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("remove expired device authorizations: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ConformsTo reports the suite version this adapter is verified against.
func (s *DeviceFlowStore) ConformsTo() string {
	return storagetest.SuiteVersion
}

// scanRecord decodes one (version, payload) row into an independent record
// whose Version comes from the version column; rows written before
// migration 000007 carry no Version in their payload. A scan error is
// returned as-is so callers can map pgx.ErrNoRows.
func (s *DeviceFlowStore) scanRecord(row pgx.Row) (*models.DeviceCode, error) {
	var version int
	var payload []byte
	if err := row.Scan(&version, &payload); err != nil {
		return nil, err
	}
	data := new(models.DeviceCode)
	if err := s.codec.decode(payload, data); err != nil {
		return nil, fmt.Errorf("decode device authorization payload: %w", err)
	}
	data.Version = version
	return data, nil
}
