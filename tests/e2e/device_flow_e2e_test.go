// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the device flow store's behavior against the real
// schema: the dual-code single-row shape (device code in handle, user code
// in user_code), the column-level expiry derivation (CreationTime plus
// Lifetime in seconds, including the zero-lifetime case), the expiry refresh
// on UpdateByUserCode so no stale expiry survives, and the zero-row no-op
// semantics of updating an absent user code. The suite is gated behind
// WICKET_PG_TEST_DATABASE_URL so the plain test run stays green without a
// database.
package e2e_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixedNow mirrors the conformance suite's fixed time base so the column
// assertions share its determinism.
var fixedNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// TestE2EDeviceFlowDualCodeColumns asserts the dual-code single-row shape
// directly at the column level: the device code lands in handle (the
// primary key), the user code in user_code, both on the same row — one row
// backs both lookup paths.
func TestE2EDeviceFlowDualCodeColumns(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewDeviceFlowStore(pool, discardLogger())

	data := &models.DeviceCode{
		CreationTime:    fixedNow,
		Lifetime:        300,
		ClientId:        "client-flow",
		Description:     "tv in the living room",
		IsOpenId:        true,
		RequestedScopes: []string{"openid", "profile"},
	}
	if err := s.StoreDeviceAuthorization(t.Context(), "device-1", "user-1", data); err != nil {
		t.Fatalf("StoreDeviceAuthorization: %v", err)
	}

	var handle, userCode string
	err := pool.QueryRow(t.Context(),
		"SELECT handle, user_code FROM device_codes WHERE handle = $1",
		"device-1").Scan(&handle, &userCode)
	if err != nil {
		t.Fatalf("query device_codes columns: %v", err)
	}
	if handle != "device-1" {
		t.Errorf("handle column = %q, want device-1", handle)
	}
	if userCode != "user-1" {
		t.Errorf("user_code column = %q, want user-1", userCode)
	}

	var rows int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM device_codes WHERE handle = $1 AND user_code = $2",
		"device-1", "user-1").Scan(&rows); err != nil {
		t.Fatalf("count dual-code row: %v", err)
	}
	if rows != 1 {
		t.Errorf("dual-code row count = %d, want 1 (single row carries both codes)", rows)
	}
}

// TestE2EDeviceFlowExpiresAtColumn asserts the expiry derivation directly
// at the column level: expires_at equals CreationTime plus Lifetime seconds,
// with no special case for a zero lifetime (the expiry point is then
// exactly CreationTime).
func TestE2EDeviceFlowExpiresAtColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewDeviceFlowStore(pool, discardLogger())

	withLifetime := &models.DeviceCode{CreationTime: fixedNow, Lifetime: 300, ClientId: "client-flow"}
	if err := s.StoreDeviceAuthorization(t.Context(), "device-ttl", "user-ttl", withLifetime); err != nil {
		t.Fatalf("StoreDeviceAuthorization (with lifetime): %v", err)
	}
	assertDeviceFlowExpiresAt(t, pool, "device-ttl", fixedNow.Add(300*time.Second))

	zeroLifetime := &models.DeviceCode{CreationTime: fixedNow, Lifetime: 0, ClientId: "client-flow"}
	if err := s.StoreDeviceAuthorization(t.Context(), "device-zero", "user-zero", zeroLifetime); err != nil {
		t.Fatalf("StoreDeviceAuthorization (zero lifetime): %v", err)
	}
	assertDeviceFlowExpiresAt(t, pool, "device-zero", fixedNow)
}

// TestE2EDeviceFlowUpdateRefreshesExpiresAt asserts the expiry refresh on
// UpdateByUserCode: the replacement data re-derives expires_at, so a stale
// expiry cannot survive an update that changes CreationTime or Lifetime.
func TestE2EDeviceFlowUpdateRefreshesExpiresAt(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewDeviceFlowStore(pool, discardLogger())

	initial := &models.DeviceCode{CreationTime: fixedNow, Lifetime: 300, ClientId: "client-flow"}
	if err := s.StoreDeviceAuthorization(t.Context(), "device-upd", "user-upd", initial); err != nil {
		t.Fatalf("StoreDeviceAuthorization: %v", err)
	}
	assertDeviceFlowExpiresAt(t, pool, "device-upd", fixedNow.Add(300*time.Second))

	updated := &models.DeviceCode{CreationTime: fixedNow, Lifetime: 600, ClientId: "client-flow", IsAuthorized: true}
	if err := s.UpdateByUserCode(t.Context(), "user-upd", updated); err != nil {
		t.Fatalf("UpdateByUserCode: %v", err)
	}
	assertDeviceFlowExpiresAt(t, pool, "device-upd", fixedNow.Add(600*time.Second))

	got, err := s.FindByUserCode(t.Context(), "user-upd")
	if err != nil {
		t.Fatalf("FindByUserCode after update: %v", err)
	}
	if !got.IsAuthorized {
		t.Errorf("IsAuthorized after update = false, want true (payload replaced)")
	}
}

// TestE2EDeviceFlowUpdateMissingUserCodeIsNoOp asserts the zero-row write
// semantics: updating an absent user code succeeds without error and does
// not create a new row.
func TestE2EDeviceFlowUpdateMissingUserCodeIsNoOp(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewDeviceFlowStore(pool, discardLogger())

	data := &models.DeviceCode{CreationTime: fixedNow, Lifetime: 300, ClientId: "client-flow"}
	if err := s.UpdateByUserCode(t.Context(), "absent-user", data); err != nil {
		t.Fatalf("UpdateByUserCode on absent user code: %v", err)
	}

	var count int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM device_codes").Scan(&count); err != nil {
		t.Fatalf("count device_codes rows: %v", err)
	}
	if count != 0 {
		t.Errorf("device_codes holds %d rows after no-op update, want 0", count)
	}

	got, err := s.FindByUserCode(t.Context(), "absent-user")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("read after no-op update error = %v, want storage.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("read after no-op update record = %v, want nil", got)
	}
}

// assertDeviceFlowExpiresAt asserts the expires_at column for the device
// code; the query condition is the single-column handle key.
func assertDeviceFlowExpiresAt(t *testing.T, pool *pgxpool.Pool, deviceCode string, want time.Time) {
	t.Helper()
	var got time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM device_codes WHERE handle = $1", deviceCode).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s: %v", deviceCode, err)
	}
	if !got.Equal(want) {
		t.Errorf("expires_at for %s = %v, want %v", deviceCode, got, want)
	}
}
