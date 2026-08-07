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

// DeviceFlowStore is the pgx-backed adapter for the wicket
// storage.DeviceFlowStore port. Each record carries both codes in one row:
// handle (= device code) is the primary key and user_code is the second
// unique lookup key, so one row backs both read paths and one deletion
// clears both codes. The model is serialized into the payload column;
// expires_at is derived at write time and reclaimed only by an explicit
// RemoveExpired call.
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
// left unchanged. expires_at is derived from the model's CreationTime and
// Lifetime, never from a clock inside the adapter.
func (s *DeviceFlowStore) StoreDeviceAuthorization(ctx context.Context, deviceCode string, userCode string, data *models.DeviceCode) error {
	payload, err := s.codec.encode(data)
	if err != nil {
		return fmt.Errorf("store device authorization payload: %w", err)
	}
	expiresAt := data.CreationTime.Add(time.Duration(data.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"INSERT INTO device_codes (handle, user_code, expires_at, payload) VALUES ($1, $2, $3, $4)",
		deviceCode, userCode, expiresAt, payload)
	if err != nil {
		return mapDuplicateErr(err, storage.ErrDuplicateHandle)
	}
	s.logger.Debug("device authorization stored",
		"device_code_prefix", handlePrefix(deviceCode),
		"user_code_prefix", handlePrefix(userCode))
	return nil
}

// FindByDeviceCode returns an independent copy of the record for the device
// code. A missing code fails with storage.ErrNotFound and a nil record; the
// result is never (nil, nil).
func (s *DeviceFlowStore) FindByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceCode, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM device_codes WHERE handle = $1", deviceCode).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	data := new(models.DeviceCode)
	if err := s.codec.decode(payload, data); err != nil {
		return nil, fmt.Errorf("decode device authorization payload: %w", err)
	}
	s.logger.Debug("device authorization read", "device_code_prefix", handlePrefix(deviceCode), "found", true)
	return data, nil
}

// FindByUserCode returns an independent copy of the record for the user
// code. A missing code fails with storage.ErrNotFound and a nil record; the
// result is never (nil, nil).
func (s *DeviceFlowStore) FindByUserCode(ctx context.Context, userCode string) (*models.DeviceCode, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx,
		"SELECT payload FROM device_codes WHERE user_code = $1", userCode).Scan(&payload)
	if err != nil {
		return nil, mapReadErr(err, storage.ErrNotFound)
	}
	data := new(models.DeviceCode)
	if err := s.codec.decode(payload, data); err != nil {
		return nil, fmt.Errorf("decode device authorization payload: %w", err)
	}
	s.logger.Debug("device authorization read", "user_code_prefix", handlePrefix(userCode), "found", true)
	return data, nil
}

// UpdateByUserCode replaces the payload and the derived expires_at for the
// record under user code. The write is a state-migration replacement, not a
// version-guarded update; updating a user code that is not present succeeds
// as a no-op.
func (s *DeviceFlowStore) UpdateByUserCode(ctx context.Context, userCode string, data *models.DeviceCode) error {
	payload, err := s.codec.encode(data)
	if err != nil {
		return fmt.Errorf("update device authorization payload: %w", err)
	}
	expiresAt := data.CreationTime.Add(time.Duration(data.Lifetime) * time.Second)
	_, err = s.pool.Exec(ctx,
		"UPDATE device_codes SET payload = $2, expires_at = $3 WHERE user_code = $1",
		userCode, payload, expiresAt)
	if err != nil {
		return fmt.Errorf("update device authorization: %w", err)
	}
	s.logger.Debug("device authorization updated", "user_code_prefix", handlePrefix(userCode))
	return nil
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
