// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the backchannel authentication request store's
// behavior against the real schema: the ExpirationTime-family expiry column
// semantics (a non-zero value lands in expires_at as-is, a zero value lands
// as NULL and is never cleaned up), the expiry refresh on
// UpdateBackchannelAuthenticationRequest so no stale expiry survives, and
// the zero-row no-op semantics of updating an absent request id. The suite
// is gated behind WICKET_PG_TEST_DATABASE_URL so the plain test run stays
// green without a database.
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

// TestE2EBackchannelExpiresAtColumn asserts the ExpirationTime-family expiry
// semantics directly at the column level: a non-zero ExpirationTime lands in
// expires_at as-is, while a zero ExpirationTime lands as NULL (never
// expires) — the exact distinction RemoveExpired's IS NOT NULL guard relies
// on.
func TestE2EBackchannelExpiresAtColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewBackchannelAuthenticationRequestStore(pool, discardLogger())

	nonZero := &models.BackchannelAuthenticationRequest{
		RequestId:      "req-1",
		ClientId:       "client-a",
		ExpirationTime: fixedAlive,
	}
	if err := s.StoreBackchannelAuthenticationRequest(t.Context(), "req-1", nonZero); err != nil {
		t.Fatalf("StoreBackchannelAuthenticationRequest (non-zero expiry): %v", err)
	}
	assertBackchannelExpiresAt(t, pool, "req-1", fixedAlive)

	zero := &models.BackchannelAuthenticationRequest{
		RequestId: "req-zero",
		ClientId:  "client-a",
	}
	if err := s.StoreBackchannelAuthenticationRequest(t.Context(), "req-zero", zero); err != nil {
		t.Fatalf("StoreBackchannelAuthenticationRequest (zero expiry): %v", err)
	}
	assertBackchannelExpiresAtNull(t, pool, "req-zero")
}

// TestE2EBackchannelUpdateRefreshesExpiresAt asserts the expiry refresh on
// UpdateBackchannelAuthenticationRequest in both directions: a non-zero
// replacement ExpirationTime moves the column to the new value, and a zero
// replacement ExpirationTime moves the column to NULL — a stale expiry
// cannot survive an update.
func TestE2EBackchannelUpdateRefreshesExpiresAt(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewBackchannelAuthenticationRequestStore(pool, discardLogger())

	initial := &models.BackchannelAuthenticationRequest{
		RequestId:      "req-upd",
		ClientId:       "client-a",
		ExpirationTime: fixedAlive,
	}
	if err := s.StoreBackchannelAuthenticationRequest(t.Context(), "req-upd", initial); err != nil {
		t.Fatalf("StoreBackchannelAuthenticationRequest: %v", err)
	}
	assertBackchannelExpiresAt(t, pool, "req-upd", fixedAlive)

	approved := &models.BackchannelAuthenticationRequest{
		RequestId:      "req-upd",
		ClientId:       "client-a",
		Status:         models.BackchannelAuthenticationRequestStatusApproved,
		ExpirationTime: fixedNow,
	}
	if err := s.UpdateBackchannelAuthenticationRequest(t.Context(), "req-upd", approved); err != nil {
		t.Fatalf("UpdateBackchannelAuthenticationRequest (non-zero expiry): %v", err)
	}
	assertBackchannelExpiresAt(t, pool, "req-upd", fixedNow)

	got, err := s.FindBackchannelAuthenticationRequest(t.Context(), "req-upd")
	if err != nil {
		t.Fatalf("FindBackchannelAuthenticationRequest after update: %v", err)
	}
	if got.Status != models.BackchannelAuthenticationRequestStatusApproved {
		t.Errorf("Status after update = %q, want approved (payload replaced)", got.Status)
	}

	expiryCleared := &models.BackchannelAuthenticationRequest{
		RequestId: "req-upd",
		ClientId:  "client-a",
		Status:    models.BackchannelAuthenticationRequestStatusConsumed,
	}
	if err := s.UpdateBackchannelAuthenticationRequest(t.Context(), "req-upd", expiryCleared); err != nil {
		t.Fatalf("UpdateBackchannelAuthenticationRequest (zero expiry): %v", err)
	}
	assertBackchannelExpiresAtNull(t, pool, "req-upd")
}

// TestE2EBackchannelUpdateMissingRequestIDIsNoOp asserts the zero-row write
// semantics: updating an absent request id succeeds without error and does
// not create a new row.
func TestE2EBackchannelUpdateMissingRequestIDIsNoOp(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewBackchannelAuthenticationRequestStore(pool, discardLogger())

	data := &models.BackchannelAuthenticationRequest{
		RequestId:      "absent",
		ClientId:       "client-a",
		ExpirationTime: fixedAlive,
	}
	if err := s.UpdateBackchannelAuthenticationRequest(t.Context(), "absent", data); err != nil {
		t.Fatalf("UpdateBackchannelAuthenticationRequest on absent request id: %v", err)
	}

	var count int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM backchannel_auth_requests").Scan(&count); err != nil {
		t.Fatalf("count backchannel_auth_requests rows: %v", err)
	}
	if count != 0 {
		t.Errorf("backchannel_auth_requests holds %d rows after no-op update, want 0", count)
	}

	got, err := s.FindBackchannelAuthenticationRequest(t.Context(), "absent")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("read after no-op update error = %v, want storage.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("read after no-op update record = %v, want nil", got)
	}
}

// TestE2EBackchannelRemoveExpiredKeepsNullRows asserts that RemoveExpired
// never touches rows whose expires_at is NULL (zero-value ExpirationTime):
// the reclaim count is zero and the row survives.
func TestE2EBackchannelRemoveExpiredKeepsNullRows(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewBackchannelAuthenticationRequestStore(pool, discardLogger())

	zero := &models.BackchannelAuthenticationRequest{
		RequestId: "req-keep",
		ClientId:  "client-a",
	}
	if err := s.StoreBackchannelAuthenticationRequest(t.Context(), "req-keep", zero); err != nil {
		t.Fatalf("StoreBackchannelAuthenticationRequest (zero expiry): %v", err)
	}

	removed, err := s.RemoveExpired(t.Context(), fixedNow)
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if removed != 0 {
		t.Errorf("RemoveExpired removed %d rows, want 0 (NULL expiry never cleaned)", removed)
	}
	assertBackchannelExpiresAtNull(t, pool, "req-keep")
}

// assertBackchannelExpiresAt asserts the expires_at column for the request
// id; the query condition is the single-column handle key.
func assertBackchannelExpiresAt(t *testing.T, pool *pgxpool.Pool, requestID string, want time.Time) {
	t.Helper()
	var got time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM backchannel_auth_requests WHERE handle = $1", requestID).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s: %v", requestID, err)
	}
	if !got.Equal(want) {
		t.Errorf("expires_at for %s = %v, want %v", requestID, got, want)
	}
}

// assertBackchannelExpiresAtNull asserts the expires_at column is NULL for
// the request id.
func assertBackchannelExpiresAtNull(t *testing.T, pool *pgxpool.Pool, requestID string) {
	t.Helper()
	var got *time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM backchannel_auth_requests WHERE handle = $1", requestID).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s: %v", requestID, err)
	}
	if got != nil {
		t.Errorf("expires_at for %s = %v, want NULL (zero-value ExpirationTime never expires)", requestID, got)
	}
}
