// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test exercises the authorization code store end to end against
// a real PostgreSQL server: the full store → read → consume lifecycle, the
// insert-only duplicate rejection, the write-time expiry derivation, the
// strict cleanup boundary, and the missing-read sentinel. The conformance
// suite pins the port contract against the wicket suite; these tests pin
// the same behaviors through the adapter itself on a throwaway database.
// The suite is gated behind WICKET_PG_TEST_DATABASE_URL so the plain test
// run stays green without a database.
package e2e_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestE2EAuthorizationCodeLifecycle walks the user-visible lifecycle on a
// real server: store a code, read it back with every field intact, consume
// it, and observe the one-time-use contract (RFC 6749 §4.1.2) — a second
// read fails with storage.ErrNotFound.
func TestE2EAuthorizationCodeLifecycle(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewAuthorizationCodeStore(pool, discardLogger())

	code := &models.AuthorizationCode{
		CreationTime:    fixedNow,
		Lifetime:        300,
		ClientId:        "client-e2e",
		IsOpenId:        true,
		RequestedScopes: []string{"openid", "profile"},
		Properties:      map[string]string{"k": "v"},
	}
	const handle = "e2e-auth-code-1"
	if err := s.StoreAuthorizationCode(t.Context(), handle, code); err != nil {
		t.Fatalf("StoreAuthorizationCode: %v", err)
	}

	got, err := s.GetAuthorizationCode(t.Context(), handle)
	if err != nil {
		t.Fatalf("GetAuthorizationCode: %v", err)
	}
	if !got.CreationTime.Equal(code.CreationTime) {
		t.Errorf("CreationTime = %v, want %v", got.CreationTime, code.CreationTime)
	}
	if got.Lifetime != code.Lifetime {
		t.Errorf("Lifetime = %d, want %d", got.Lifetime, code.Lifetime)
	}
	if got.ClientId != code.ClientId {
		t.Errorf("ClientId = %q, want %q", got.ClientId, code.ClientId)
	}
	if got.IsOpenId != code.IsOpenId {
		t.Errorf("IsOpenId = %v, want %v", got.IsOpenId, code.IsOpenId)
	}
	if len(got.RequestedScopes) != len(code.RequestedScopes) {
		t.Errorf("RequestedScopes = %v, want %v", got.RequestedScopes, code.RequestedScopes)
	} else {
		for i := range code.RequestedScopes {
			if got.RequestedScopes[i] != code.RequestedScopes[i] {
				t.Errorf("RequestedScopes[%d] = %q, want %q", i, got.RequestedScopes[i], code.RequestedScopes[i])
			}
		}
	}
	if v, ok := got.Properties["k"]; !ok || v != "v" {
		t.Errorf("Properties = %v, want %v", got.Properties, code.Properties)
	}

	if err := s.RemoveAuthorizationCode(t.Context(), handle); err != nil {
		t.Fatalf("RemoveAuthorizationCode: %v", err)
	}
	missing, err := s.GetAuthorizationCode(t.Context(), handle)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("read after consume error = %v, want storage.ErrNotFound", err)
	}
	if missing != nil {
		t.Errorf("read after consume record = %v, want nil", missing)
	}
}

// TestE2EAuthorizationCodeDuplicateHandleRejected pins the insert-only
// write semantics: a second store under an existing handle fails with
// storage.ErrDuplicateHandle and leaves the original record untouched.
func TestE2EAuthorizationCodeDuplicateHandleRejected(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewAuthorizationCodeStore(pool, discardLogger())

	first := &models.AuthorizationCode{
		CreationTime: fixedNow,
		Lifetime:     60,
		ClientId:     "client-first",
	}
	second := &models.AuthorizationCode{
		CreationTime: fixedNow.Add(time.Hour),
		Lifetime:     60,
		ClientId:     "client-second",
	}
	const handle = "e2e-auth-code-dup"
	if err := s.StoreAuthorizationCode(t.Context(), handle, first); err != nil {
		t.Fatalf("StoreAuthorizationCode (first): %v", err)
	}
	if err := s.StoreAuthorizationCode(t.Context(), handle, second); !errors.Is(err, storage.ErrDuplicateHandle) {
		t.Errorf("StoreAuthorizationCode (duplicate) error = %v, want storage.ErrDuplicateHandle", err)
	}

	got, err := s.GetAuthorizationCode(t.Context(), handle)
	if err != nil {
		t.Fatalf("GetAuthorizationCode after duplicate: %v", err)
	}
	if got.ClientId != first.ClientId {
		t.Errorf("ClientId after rejected duplicate = %q, want %q (original record mutated)", got.ClientId, first.ClientId)
	}
	if !got.CreationTime.Equal(first.CreationTime) {
		t.Errorf("CreationTime after rejected duplicate = %v, want %v", got.CreationTime, first.CreationTime)
	}
}

// TestE2EAuthorizationCodeExpiresAtDerivedAtWrite verifies the derived
// expiry column against the real schema: expires_at holds
// CreationTime + Lifetime seconds with no zero-lifetime special case (a
// zero lifetime makes the expiry instant equal the creation instant).
func TestE2EAuthorizationCodeExpiresAtDerivedAtWrite(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewAuthorizationCodeStore(pool, discardLogger())

	creation := fixedNow

	if err := s.StoreAuthorizationCode(t.Context(), "e2e-exp-zero", &models.AuthorizationCode{
		CreationTime: creation,
		Lifetime:     0,
		ClientId:     "c",
	}); err != nil {
		t.Fatalf("StoreAuthorizationCode (zero lifetime): %v", err)
	}
	assertExpiresAt(t, pool, "e2e-exp-zero", creation)

	if err := s.StoreAuthorizationCode(t.Context(), "e2e-exp-300", &models.AuthorizationCode{
		CreationTime: creation,
		Lifetime:     300,
		ClientId:     "c",
	}); err != nil {
		t.Fatalf("StoreAuthorizationCode (300s lifetime): %v", err)
	}
	assertExpiresAt(t, pool, "e2e-exp-300", creation.Add(300*time.Second))
}

// TestE2EAuthorizationCodeRemoveExpiredBoundaries pins the cleanup
// semantics: RemoveExpired reclaims only records whose expiry strictly
// precedes the caller-supplied cutoff — the boundary record (expires_at ==
// cutoff) survives, expired records stay readable until reclaimed, and the
// removed count matches exactly.
func TestE2EAuthorizationCodeRemoveExpiredBoundaries(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewAuthorizationCodeStore(pool, discardLogger())

	codes := []struct {
		handle   string
		creation time.Time
		lifetime int
	}{
		{"e2e-expired", fixedNow.Add(-120 * time.Second), 60}, // expires fixedNow - 60s
		{"e2e-boundary", fixedNow.Add(-60 * time.Second), 60}, // expires fixedNow, must survive
		{"e2e-alive", fixedNow, 3600},                         // expires fixedNow + 1h
	}
	for _, c := range codes {
		if err := s.StoreAuthorizationCode(t.Context(), c.handle, &models.AuthorizationCode{
			CreationTime: c.creation,
			Lifetime:     c.lifetime,
			ClientId:     "c",
		}); err != nil {
			t.Fatalf("StoreAuthorizationCode (%s): %v", c.handle, err)
		}
	}

	// An expired record remains readable until cleanup — the store does no
	// expiry visibility filtering of its own.
	if _, err := s.GetAuthorizationCode(t.Context(), "e2e-expired"); err != nil {
		t.Errorf("GetAuthorizationCode (expired, pre-cleanup) = %v, want nil", err)
	}

	n, err := s.RemoveExpired(t.Context(), fixedNow)
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired reclaimed %d, want 1", n)
	}

	if _, err := s.GetAuthorizationCode(t.Context(), "e2e-boundary"); err != nil {
		t.Errorf("boundary record removed: %v", err)
	}
	if _, err := s.GetAuthorizationCode(t.Context(), "e2e-alive"); err != nil {
		t.Errorf("alive record removed: %v", err)
	}
	if _, err := s.GetAuthorizationCode(t.Context(), "e2e-expired"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expired record after cleanup error = %v, want storage.ErrNotFound", err)
	}

	// A later cutoff reclaims the survivors: nothing beyond expires_at was
	// reclaimed early.
	n, err = s.RemoveExpired(t.Context(), fixedNow.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("RemoveExpired (later cutoff): %v", err)
	}
	if n != 2 {
		t.Errorf("RemoveExpired (later cutoff) reclaimed %d, want 2", n)
	}
}

// TestE2EAuthorizationCodeMissingReadReturnsSentinel pins the missing-read
// contract on a real server: an unknown handle fails with
// storage.ErrNotFound and a nil record — never (nil, nil).
func TestE2EAuthorizationCodeMissingReadReturnsSentinel(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewAuthorizationCodeStore(pool, discardLogger())

	got, err := s.GetAuthorizationCode(t.Context(), "e2e-never-stored")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("missing read error = %v, want storage.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("missing read record = %v, want nil", got)
	}
}

// assertExpiresAt asserts the expires_at column value for handle.
func assertExpiresAt(t *testing.T, pool *pgxpool.Pool, handle string, want time.Time) {
	t.Helper()
	var got time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM authorization_codes WHERE handle = $1", handle).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s: %v", handle, err)
	}
	if !got.Equal(want) {
		t.Errorf("expires_at for %s = %v, want %v", handle, got, want)
	}
}

// discardLogger keeps e2e output clean; store adapters must not emit
// credentials at any level, so an io.Discard handler is the safe choice.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
