// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the user consent store's behavior against the real
// schema: the full store → read → list → remove lifecycle, the missing-read
// sentinel, the cleanup counting and boundary, plus the column-level
// behaviors the conformance suite cannot see — expires_at holds the
// consent's Expiration pointer instant verbatim (NULL for a nil pointer,
// meaning never cleaned up), and the upsert conflict branch refreshes
// expires_at so no stale value survives. The suite is gated behind
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

// TestE2EUserConsentLifecycle walks the user-visible lifecycle on a real
// server: store a consent, read it back with every field intact, store a
// second consent for the same subject, list them back, remove one, and
// observe the missing pair fail with storage.ErrNotFound.
func TestE2EUserConsentLifecycle(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewUserConsentStore(pool, discardLogger())

	expiration := fixedAlive
	consent := &models.Consent{
		SubjectId:    "subject-lifecycle",
		ClientId:     "client-a",
		Scopes:       []string{"openid", "profile"},
		CreationTime: fixedNow,
		Expiration:   &expiration,
	}
	if err := s.StoreUserConsent(t.Context(), consent); err != nil {
		t.Fatalf("StoreUserConsent: %v", err)
	}

	got, err := s.GetUserConsent(t.Context(), consent.SubjectId, consent.ClientId)
	if err != nil {
		t.Fatalf("GetUserConsent: %v", err)
	}
	if got.SubjectId != consent.SubjectId || got.ClientId != consent.ClientId {
		t.Errorf("natural key = %s/%s, want %s/%s", got.SubjectId, got.ClientId, consent.SubjectId, consent.ClientId)
	}
	if len(got.Scopes) != len(consent.Scopes) {
		t.Errorf("Scopes = %v, want %v", got.Scopes, consent.Scopes)
	} else {
		for i := range consent.Scopes {
			if got.Scopes[i] != consent.Scopes[i] {
				t.Errorf("Scopes[%d] = %q, want %q", i, got.Scopes[i], consent.Scopes[i])
			}
		}
	}
	if !got.CreationTime.Equal(consent.CreationTime) {
		t.Errorf("CreationTime = %v, want %v", got.CreationTime, consent.CreationTime)
	}
	if got.Expiration == nil || !got.Expiration.Equal(*consent.Expiration) {
		t.Errorf("Expiration = %v, want %v", got.Expiration, *consent.Expiration)
	}

	second := &models.Consent{
		SubjectId:    consent.SubjectId,
		ClientId:     "client-b",
		Scopes:       []string{"offline_access"},
		CreationTime: consent.CreationTime,
	}
	if err := s.StoreUserConsent(t.Context(), second); err != nil {
		t.Fatalf("StoreUserConsent (second client): %v", err)
	}

	all, err := s.GetAllUserConsents(t.Context(), consent.SubjectId)
	if err != nil {
		t.Fatalf("GetAllUserConsents: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAllUserConsents = %d consents, want 2", len(all))
	}

	if err := s.RemoveUserConsent(t.Context(), consent.SubjectId, consent.ClientId); err != nil {
		t.Fatalf("RemoveUserConsent: %v", err)
	}
	missing, err := s.GetUserConsent(t.Context(), consent.SubjectId, consent.ClientId)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("read after remove error = %v, want storage.ErrNotFound", err)
	}
	if missing != nil {
		t.Errorf("read after remove record = %v, want nil", missing)
	}

	all, err = s.GetAllUserConsents(t.Context(), consent.SubjectId)
	if err != nil {
		t.Fatalf("GetAllUserConsents after remove: %v", err)
	}
	if len(all) != 1 || all[0].ClientId != second.ClientId {
		t.Errorf("GetAllUserConsents after remove = %d consents (%v), want only client-b", len(all), all)
	}
}

// TestE2EUserConsentMissingReadReturnsSentinel pins the missing-read
// contract on a real server: an unknown pair fails with storage.ErrNotFound
// and a nil record — never (nil, nil).
func TestE2EUserConsentMissingReadReturnsSentinel(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewUserConsentStore(pool, discardLogger())

	got, err := s.GetUserConsent(t.Context(), "absent-subject", "absent-client")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("missing read error = %v, want storage.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("missing read record = %v, want nil", got)
	}
}

// TestE2EUserConsentRemoveExpiredCounting pins the cleanup semantics: only
// records whose expiry strictly precedes the caller-supplied cutoff are
// reclaimed — the boundary record (expires_at == cutoff) and records with
// NULL expires_at (never expiring) survive, and the removed count matches
// exactly.
func TestE2EUserConsentRemoveExpiredCounting(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewUserConsentStore(pool, discardLogger())

	expired := fixedExpired
	boundary := fixedNow
	base := models.Consent{
		SubjectId:    "subject-cleanup",
		CreationTime: fixedNow,
		Scopes:       []string{"openid"},
	}
	pairs := []struct {
		clientId string
		expiry   *time.Time
	}{
		{"expired", &expired},
		{"boundary", &boundary}, // expires_at == cutoff, must survive
		{"alive", nil},          // never expiring, must survive
	}
	for _, p := range pairs {
		c := base
		c.ClientId = p.clientId
		c.Expiration = p.expiry
		if err := s.StoreUserConsent(t.Context(), &c); err != nil {
			t.Fatalf("StoreUserConsent (%s): %v", p.clientId, err)
		}
	}

	n, err := s.RemoveExpired(t.Context(), fixedNow)
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired reclaimed %d, want 1", n)
	}

	for _, clientId := range []string{"boundary", "alive"} {
		if _, err := s.GetUserConsent(t.Context(), base.SubjectId, clientId); err != nil {
			t.Errorf("consent %s removed: %v", clientId, err)
		}
	}
	if _, err := s.GetUserConsent(t.Context(), base.SubjectId, "expired"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expired consent after cleanup error = %v, want storage.ErrNotFound", err)
	}

	// A later cutoff reclaims the survivors: nothing was reclaimed early.
	n, err = s.RemoveExpired(t.Context(), fixedNow.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("RemoveExpired (later cutoff): %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired (later cutoff) reclaimed %d, want 1", n)
	}
	if _, err := s.GetUserConsent(t.Context(), base.SubjectId, "alive"); err != nil {
		t.Errorf("never-expiring consent removed by later cutoff: %v", err)
	}
}

// TestE2EUserConsentExpiryColumn walks store on a real server and asserts
// the user_consents expires_at column directly: a non-nil Expiration is
// stored verbatim, and a nil Expiration stores NULL (never cleaned up).
func TestE2EUserConsentExpiryColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewUserConsentStore(pool, discardLogger())

	instant := fixedAlive
	consent := &models.Consent{
		SubjectId:    "subject-e2e",
		ClientId:     "client-e2e",
		Scopes:       []string{"openid"},
		CreationTime: fixedNow,
		Expiration:   &instant,
	}
	if err := s.StoreUserConsent(t.Context(), consent); err != nil {
		t.Fatalf("StoreUserConsent (with expiry): %v", err)
	}
	assertUserConsentExpiresAt(t, pool, "subject-e2e", "client-e2e", &instant)

	consent.Expiration = nil
	if err := s.StoreUserConsent(t.Context(), consent); err != nil {
		t.Fatalf("StoreUserConsent (no expiry): %v", err)
	}
	assertUserConsentExpiresAt(t, pool, "subject-e2e", "client-e2e", nil)
}

// TestE2EUserConsentUpsertRefreshesExpiry asserts the AD-10 gap: the upsert
// conflict branch refreshes expires_at together with payload, so an earlier
// expiry cannot linger on a record that was later stored without one.
func TestE2EUserConsentUpsertRefreshesExpiry(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewUserConsentStore(pool, discardLogger())

	alive := fixedAlive
	base := &models.Consent{
		SubjectId:    "subject-upsert",
		ClientId:     "client-upsert",
		Scopes:       []string{"openid"},
		CreationTime: fixedNow,
	}

	// Expiry first, then nil: the stale expiry must not survive the second
	// write, or RemoveExpired would later reclaim a record stored as never
	// expiring.
	withExpiry := *base
	withExpiry.Expiration = &alive
	if err := s.StoreUserConsent(t.Context(), &withExpiry); err != nil {
		t.Fatalf("StoreUserConsent (expiry first): %v", err)
	}
	if err := s.StoreUserConsent(t.Context(), base); err != nil {
		t.Fatalf("StoreUserConsent (nil expiry second): %v", err)
	}
	assertUserConsentExpiresAt(t, pool, "subject-upsert", "client-upsert", nil)

	// Reverse: nil first, then a real instant — the column must hold the
	// new instant.
	if err := s.StoreUserConsent(t.Context(), base); err != nil {
		t.Fatalf("StoreUserConsent (nil first): %v", err)
	}
	if err := s.StoreUserConsent(t.Context(), &withExpiry); err != nil {
		t.Fatalf("StoreUserConsent (expiry second): %v", err)
	}
	assertUserConsentExpiresAt(t, pool, "subject-upsert", "client-upsert", &alive)
}

// TestE2EUserConsentRemoveMissingPairIsNoOp asserts the write-method
// zero-row semantics: removing a pair that was never stored succeeds and
// leaves no row behind.
func TestE2EUserConsentRemoveMissingPairIsNoOp(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewUserConsentStore(pool, discardLogger())

	if err := s.RemoveUserConsent(t.Context(), "absent-subject", "absent-client"); err != nil {
		t.Fatalf("RemoveUserConsent on missing pair: %v", err)
	}

	consents, err := s.GetAllUserConsents(t.Context(), "absent-subject")
	if err != nil {
		t.Fatalf("GetAllUserConsents after no-op remove: %v", err)
	}
	if len(consents) != 0 {
		t.Errorf("GetAllUserConsents after no-op remove = %d rows, want 0", len(consents))
	}
}

// assertUserConsentExpiresAt asserts the expires_at column for the natural
// key pair; want nil asserts a NULL column. The authorization code variant
// is hardcoded to its own table and single-handle key, so this variant
// targets user_consents with the composite key.
func assertUserConsentExpiresAt(t *testing.T, pool *pgxpool.Pool, subjectId, clientId string, want *time.Time) {
	t.Helper()
	var got *time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM user_consents WHERE subject_id = $1 AND client_id = $2",
		subjectId, clientId).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s/%s: %v", subjectId, clientId, err)
	}
	switch {
	case want == nil && got != nil:
		t.Errorf("expires_at for %s/%s = %v, want NULL", subjectId, clientId, *got)
	case want != nil && got == nil:
		t.Errorf("expires_at for %s/%s = NULL, want %v", subjectId, clientId, want)
	case want != nil && !got.Equal(*want):
		t.Errorf("expires_at for %s/%s = %v, want %v", subjectId, clientId, *got, *want)
	}
}
