// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the refresh token store's column-level behavior
// against the real schema: the version column mirrors the payload-carrying
// copy (1 after store, expectedVersion+1 after a successful optimistic
// update) and the expires_at column holds CreationTime + Lifetime seconds
// with no zero-lifetime special case. The conformance suite asserts these
// indirectly through GetRefreshToken; these tests assert the columns
// themselves. The suite is gated behind WICKET_PG_TEST_DATABASE_URL so the
// plain test run stays green without a database.
package e2e_test

import (
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestE2ERefreshTokenVersionAndExpiryColumns walks store → update on a
// real server and asserts the refresh_tokens columns directly: version is
// 1 after store and 2 after a successful update at expectedVersion 1, and
// expires_at tracks the caller-provided CreationTime + Lifetime seconds
// on both paths (zero lifetime keeps the expiry instant at creation).
func TestE2ERefreshTokenVersionAndExpiryColumns(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewRefreshTokenStore(pool, discardLogger())

	creation := fixedNow

	token := &models.RefreshToken{
		CreationTime: creation,
		Lifetime:     300,
		ClientId:     "client-e2e",
	}
	const handle = "e2e-refresh-1"
	if err := s.StoreRefreshToken(t.Context(), handle, token); err != nil {
		t.Fatalf("StoreRefreshToken: %v", err)
	}
	if token.Version != 1 {
		t.Errorf("caller Version after store = %d, want 1", token.Version)
	}
	assertRefreshTokenColumns(t, pool, handle, 1, creation.Add(300*time.Second))

	rotated := &models.RefreshToken{
		CreationTime: creation,
		Lifetime:     600,
		ClientId:     "client-e2e",
	}
	if err := s.UpdateRefreshToken(t.Context(), handle, rotated, 1); err != nil {
		t.Fatalf("UpdateRefreshToken: %v", err)
	}
	if rotated.Version != 2 {
		t.Errorf("caller Version after update = %d, want 2", rotated.Version)
	}
	assertRefreshTokenColumns(t, pool, handle, 2, creation.Add(600*time.Second))
}

// TestE2ERefreshTokenZeroLifetimeColumn pins the no-special-case expiry
// derivation: a zero lifetime makes expires_at equal CreationTime.
func TestE2ERefreshTokenZeroLifetimeColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewRefreshTokenStore(pool, discardLogger())

	creation := fixedNow
	if err := s.StoreRefreshToken(t.Context(), "e2e-refresh-zero", &models.RefreshToken{
		CreationTime: creation,
		Lifetime:     0,
		ClientId:     "c",
	}); err != nil {
		t.Fatalf("StoreRefreshToken (zero lifetime): %v", err)
	}
	assertRefreshTokenColumns(t, pool, "e2e-refresh-zero", 1, creation)
}

// assertRefreshTokenColumns asserts the version and expires_at columns for
// handle; the authorization code variant in authorization_code_e2e_test.go
// is hardcoded to its own table, so this variant targets refresh_tokens.
func assertRefreshTokenColumns(t *testing.T, pool *pgxpool.Pool, handle string, wantVersion int, wantExpiry time.Time) {
	t.Helper()
	var version int
	var expiresAt time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT version, expires_at FROM refresh_tokens WHERE handle = $1", handle).Scan(&version, &expiresAt)
	if err != nil {
		t.Fatalf("query columns for %s: %v", handle, err)
	}
	if version != wantVersion {
		t.Errorf("version for %s = %d, want %d", handle, version, wantVersion)
	}
	if !expiresAt.Equal(wantExpiry) {
		t.Errorf("expires_at for %s = %v, want %v", handle, expiresAt, wantExpiry)
	}
}
