// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the reference token store's column-level behavior
// against the real schema: the expires_at column holds CreationTime +
// Lifetime seconds with no zero-lifetime special case. The conformance
// suite asserts this indirectly through RemoveExpired's counting; this test
// asserts the column itself. The suite is gated behind
// WICKET_PG_TEST_DATABASE_URL so the plain test run stays green without a
// database.
package e2e_test

import (
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestE2EReferenceTokenExpiryColumn walks store on a real server and
// asserts the reference_tokens expires_at column directly: the derived
// instant is CreationTime + Lifetime seconds, and a zero lifetime keeps
// the expiry instant at creation (no special case).
func TestE2EReferenceTokenExpiryColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewReferenceTokenStore(pool, discardLogger())

	creation := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	const handle = "e2e-ref-token-1"
	if err := s.StoreReferenceToken(t.Context(), handle, &models.Token{
		CreationTime: creation,
		Lifetime:     300,
		ClientId:     "client-e2e",
	}); err != nil {
		t.Fatalf("StoreReferenceToken: %v", err)
	}
	assertReferenceTokenExpiresAt(t, pool, handle, creation.Add(300*time.Second))

	if err := s.StoreReferenceToken(t.Context(), "e2e-ref-token-zero", &models.Token{
		CreationTime: creation,
		Lifetime:     0,
		ClientId:     "client-e2e",
	}); err != nil {
		t.Fatalf("StoreReferenceToken (zero lifetime): %v", err)
	}
	assertReferenceTokenExpiresAt(t, pool, "e2e-ref-token-zero", creation)
}

// assertReferenceTokenExpiresAt asserts the expires_at column for handle;
// the authorization code variant in authorization_code_e2e_test.go is
// hardcoded to its own table, so this variant targets reference_tokens.
func assertReferenceTokenExpiresAt(t *testing.T, pool *pgxpool.Pool, handle string, want time.Time) {
	t.Helper()
	var got time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM reference_tokens WHERE handle = $1", handle).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s: %v", handle, err)
	}
	if !got.Equal(want) {
		t.Errorf("expires_at for %s = %v, want %v", handle, got, want)
	}
}
