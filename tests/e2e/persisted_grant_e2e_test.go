// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the persisted grant store's behavior against the
// real schema: the full store → read → list → remove lifecycle, the
// missing-read sentinel, the upsert conflict branch refreshing every real
// column (filter dimensions and expires_at) so no stale value survives, the
// column-level expiry semantics (non-nil Expiration stored verbatim, nil
// stored as NULL), the = ANY(...) multi-value filter paths the conformance
// suite never exercises, and the zero-row no-op semantics of the write
// methods. The suite is gated behind WICKET_PG_TEST_DATABASE_URL so the
// plain test run stays green without a database.
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

// TestE2EPersistedGrantLifecycle walks the user-visible lifecycle on a real
// server: store a grant, read it back with every field intact, list grants
// by subject, remove one, and observe the missing key fail with
// storage.ErrNotFound.
func TestE2EPersistedGrantLifecycle(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	expiration := fixedAlive
	grant := &models.PersistedGrant{
		Key:          "grant-lifecycle",
		Type:         "refresh_token",
		SubjectId:    "subject-lifecycle",
		SessionId:    "session-lifecycle",
		ClientId:     "client-a",
		Description:  "test grant",
		CreationTime: fixedNow,
		Expiration:   &expiration,
		Data:         []byte("grant-data"),
	}
	if err := s.Store(t.Context(), grant); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := s.Get(t.Context(), grant.Key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != grant.Key || got.Type != grant.Type || got.SubjectId != grant.SubjectId ||
		got.SessionId != grant.SessionId || got.ClientId != grant.ClientId || got.Description != grant.Description {
		t.Errorf("grant fields = key %q type %q subject %q session %q client %q desc %q, want the stored values",
			got.Key, got.Type, got.SubjectId, got.SessionId, got.ClientId, got.Description)
	}
	if !got.CreationTime.Equal(grant.CreationTime) {
		t.Errorf("CreationTime = %v, want %v", got.CreationTime, grant.CreationTime)
	}
	if got.Expiration == nil || !got.Expiration.Equal(*grant.Expiration) {
		t.Errorf("Expiration = %v, want %v", got.Expiration, *grant.Expiration)
	}
	if string(got.Data) != "grant-data" {
		t.Errorf("Data = %q, want %q", got.Data, grant.Data)
	}

	second := &models.PersistedGrant{
		Key:          "grant-lifecycle-2",
		Type:         "authorization_code",
		SubjectId:    grant.SubjectId,
		SessionId:    "session-2",
		ClientId:     "client-b",
		CreationTime: grant.CreationTime,
		Data:         []byte("second"),
	}
	if err := s.Store(t.Context(), second); err != nil {
		t.Fatalf("Store (second grant): %v", err)
	}

	all, err := s.GetAll(t.Context(), &storage.PersistedGrantFilter{SubjectId: grant.SubjectId})
	if err != nil {
		t.Fatalf("GetAll by subject: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAll by subject = %d grants, want 2", len(all))
	}

	if err := s.Remove(t.Context(), grant.Key); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	missing, err := s.Get(t.Context(), grant.Key)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("read after remove error = %v, want storage.ErrNotFound", err)
	}
	if missing != nil {
		t.Errorf("read after remove record = %v, want nil", missing)
	}

	all, err = s.GetAll(t.Context(), &storage.PersistedGrantFilter{SubjectId: grant.SubjectId})
	if err != nil {
		t.Fatalf("GetAll after remove: %v", err)
	}
	if len(all) != 1 || all[0].Key != second.Key {
		t.Errorf("GetAll after remove = %d grants (%v), want only %s", len(all), all, second.Key)
	}
}

// TestE2EPersistedGrantMissingReadReturnsSentinel pins the missing-read
// contract on a real server: an unknown key fails with storage.ErrNotFound
// and a nil record — never (nil, nil).
func TestE2EPersistedGrantMissingReadReturnsSentinel(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	got, err := s.Get(t.Context(), "absent-key")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("missing read error = %v, want storage.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("missing read record = %v, want nil", got)
	}
}

// TestE2EPersistedGrantRemoveExpiredCounting pins the cleanup semantics:
// only records whose stored expiry strictly precedes the caller-supplied
// cutoff are reclaimed — the boundary record (expires_at == cutoff) and
// records with NULL expires_at (never expiring) survive, and the removed
// count matches exactly.
func TestE2EPersistedGrantRemoveExpiredCounting(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	expired := fixedExpired
	boundary := fixedNow
	base := models.PersistedGrant{
		Type:         "refresh_token",
		SubjectId:    "subject-cleanup",
		SessionId:    "session-cleanup",
		ClientId:     "client-cleanup",
		CreationTime: fixedNow,
	}
	grants := []struct {
		key    string
		expiry *time.Time
	}{
		{"expired", &expired},
		{"boundary", &boundary}, // expires_at == cutoff, must survive
		{"alive", nil},          // never expiring, must survive
	}
	for _, g := range grants {
		gr := base
		gr.Key = g.key
		gr.Expiration = g.expiry
		if err := s.Store(t.Context(), &gr); err != nil {
			t.Fatalf("Store (%s): %v", g.key, err)
		}
	}

	n, err := s.RemoveExpired(t.Context(), fixedNow)
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired reclaimed %d, want 1", n)
	}

	for _, key := range []string{"boundary", "alive"} {
		if _, err := s.Get(t.Context(), key); err != nil {
			t.Errorf("grant %s removed: %v", key, err)
		}
	}
	if _, err := s.Get(t.Context(), "expired"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expired grant after cleanup error = %v, want storage.ErrNotFound", err)
	}

	// A later cutoff reclaims the survivors: nothing was reclaimed early.
	n, err = s.RemoveExpired(t.Context(), fixedNow.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("RemoveExpired (later cutoff): %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired (later cutoff) reclaimed %d, want 1", n)
	}
	if _, err := s.Get(t.Context(), "alive"); err != nil {
		t.Errorf("never-expiring grant removed by later cutoff: %v", err)
	}
}

// TestE2EPersistedGrantUpsertRefreshesFilterColumns asserts the AD-10 gap
// directly at the column level: the upsert conflict branch refreshes the
// filter dimension columns, not just the payload, so a stale subject_id,
// type, or client_id cannot survive a re-store of the same key and poison
// later filter queries.
func TestE2EPersistedGrantUpsertRefreshesFilterColumns(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	base := models.PersistedGrant{
		Key:          "grant-upsert",
		Type:         "refresh_token",
		SubjectId:    "subject-A",
		SessionId:    "session-A",
		ClientId:     "client-a",
		CreationTime: fixedNow,
		Data:         []byte("first"),
	}
	if err := s.Store(t.Context(), &base); err != nil {
		t.Fatalf("Store (first): %v", err)
	}

	updated := base
	updated.SubjectId = "subject-B"
	updated.SessionId = "session-B"
	updated.ClientId = "client-b"
	updated.Type = "authorization_code"
	updated.Data = []byte("second")
	if err := s.Store(t.Context(), &updated); err != nil {
		t.Fatalf("Store (second): %v", err)
	}

	var subjectID, sessionID, clientID, typ string
	err := pool.QueryRow(t.Context(),
		"SELECT subject_id, session_id, client_id, type FROM persisted_grants WHERE key = $1",
		"grant-upsert").Scan(&subjectID, &sessionID, &clientID, &typ)
	if err != nil {
		t.Fatalf("query filter columns: %v", err)
	}
	if subjectID != "subject-B" {
		t.Errorf("subject_id after upsert = %q, want subject-B (stale filter column)", subjectID)
	}
	if sessionID != "session-B" {
		t.Errorf("session_id after upsert = %q, want session-B (stale filter column)", sessionID)
	}
	if clientID != "client-b" {
		t.Errorf("client_id after upsert = %q, want client-b (stale filter column)", clientID)
	}
	if typ != "authorization_code" {
		t.Errorf("type after upsert = %q, want authorization_code (stale filter column)", typ)
	}

	// The stale dimension must not match any filter query anymore.
	all, err := s.GetAll(t.Context(), &storage.PersistedGrantFilter{SubjectId: "subject-A"})
	if err != nil {
		t.Fatalf("GetAll by stale subject: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("GetAll by stale subject = %d grants, want 0 (filter column not refreshed)", len(all))
	}
}

// TestE2EPersistedGrantExpiryColumn walks store on a real server and
// asserts the persisted_grants expires_at column directly: a non-nil
// Expiration is stored verbatim, and a nil Expiration stores NULL (never
// cleaned up).
func TestE2EPersistedGrantExpiryColumn(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	instant := fixedAlive
	grant := &models.PersistedGrant{
		Key:          "grant-e2e",
		Type:         "refresh_token",
		SubjectId:    "subject-e2e",
		SessionId:    "session-e2e",
		ClientId:     "client-e2e",
		CreationTime: fixedNow,
		Expiration:   &instant,
	}
	if err := s.Store(t.Context(), grant); err != nil {
		t.Fatalf("Store (with expiry): %v", err)
	}
	assertPersistedGrantExpiresAt(t, pool, "grant-e2e", &instant)

	grant.Expiration = nil
	if err := s.Store(t.Context(), grant); err != nil {
		t.Fatalf("Store (no expiry): %v", err)
	}
	assertPersistedGrantExpiresAt(t, pool, "grant-e2e", nil)
}

// TestE2EPersistedGrantUpsertRefreshesExpiry asserts the AD-10 gap: the
// upsert conflict branch refreshes expires_at together with the other real
// columns, so an earlier expiry cannot linger on a record that was later
// stored without one, and a later non-nil expiry replaces an earlier NULL.
func TestE2EPersistedGrantUpsertRefreshesExpiry(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	alive := fixedAlive
	base := &models.PersistedGrant{
		Key:          "grant-upsert-expiry",
		Type:         "refresh_token",
		SubjectId:    "subject-upsert",
		SessionId:    "session-upsert",
		ClientId:     "client-upsert",
		CreationTime: fixedNow,
	}

	// Expiry first, then nil: the stale expiry must not survive the second
	// write, or RemoveExpired would later reclaim a record stored as never
	// expiring.
	withExpiry := *base
	withExpiry.Expiration = &alive
	if err := s.Store(t.Context(), &withExpiry); err != nil {
		t.Fatalf("Store (expiry first): %v", err)
	}
	if err := s.Store(t.Context(), base); err != nil {
		t.Fatalf("Store (nil expiry second): %v", err)
	}
	assertPersistedGrantExpiresAt(t, pool, "grant-upsert-expiry", nil)

	// Reverse: nil first, then a real instant — the column must hold the
	// new instant.
	if err := s.Store(t.Context(), base); err != nil {
		t.Fatalf("Store (nil first): %v", err)
	}
	if err := s.Store(t.Context(), &withExpiry); err != nil {
		t.Fatalf("Store (expiry second): %v", err)
	}
	assertPersistedGrantExpiresAt(t, pool, "grant-upsert-expiry", &alive)
}

// TestE2EPersistedGrantMultiValueFilter exercises the = ANY(...) paths the
// conformance suite never covers: a ClientIds filter matches every grant
// whose client_id is in the list, a Types filter matches every grant whose
// type is in the list, and the two compose with AND semantics.
func TestE2EPersistedGrantMultiValueFilter(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	base := models.PersistedGrant{
		Type:         "refresh_token",
		SubjectId:    "subject-multi",
		SessionId:    "session-multi",
		CreationTime: fixedNow,
	}
	grants := []struct {
		key    string
		client string
		typ    string
	}{
		{"key-a", "client-a", "refresh_token"},
		{"key-b", "client-b", "refresh_token"},
		{"key-c", "client-a", "authorization_code"},
	}
	for _, g := range grants {
		gr := base
		gr.Key = g.key
		gr.ClientId = g.client
		gr.Type = g.typ
		if err := s.Store(t.Context(), &gr); err != nil {
			t.Fatalf("Store (%s): %v", g.key, err)
		}
	}

	all, err := s.GetAll(t.Context(), &storage.PersistedGrantFilter{ClientIds: []string{"client-a"}})
	if err != nil {
		t.Fatalf("GetAll by ClientIds: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("GetAll by ClientIds = %d grants, want 2", len(all))
	}
	for _, g := range all {
		if g.ClientId != "client-a" {
			t.Errorf("GetAll by ClientIds returned client %q, want client-a", g.ClientId)
		}
	}

	all, err = s.GetAll(t.Context(), &storage.PersistedGrantFilter{Types: []string{"authorization_code"}})
	if err != nil {
		t.Fatalf("GetAll by Types: %v", err)
	}
	if len(all) != 1 || all[0].Key != "key-c" {
		t.Errorf("GetAll by Types = %d grants (%v), want only key-c", len(all), all)
	}

	all, err = s.GetAll(t.Context(), &storage.PersistedGrantFilter{
		ClientIds: []string{"client-a"},
		Types:     []string{"refresh_token", "authorization_code"},
	})
	if err != nil {
		t.Fatalf("GetAll by ClientIds+Types: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("GetAll by ClientIds+Types = %d grants, want 2", len(all))
	}

	// RemoveAll with a multi-value filter revokes exactly the matches.
	if err := s.RemoveAll(t.Context(), &storage.PersistedGrantFilter{ClientIds: []string{"client-a"}}); err != nil {
		t.Fatalf("RemoveAll by ClientIds: %v", err)
	}
	all, err = s.GetAll(t.Context(), nil)
	if err != nil {
		t.Fatalf("GetAll after RemoveAll: %v", err)
	}
	if len(all) != 1 || all[0].Key != "key-b" {
		t.Errorf("grants after RemoveAll by ClientIds = %d (%v), want only key-b", len(all), all)
	}
}

// TestE2EPersistedGrantRemoveAllNoMatchIsNoOp asserts the write-method
// zero-row semantics: a filter matching no grant succeeds without error and
// leaves every record behind.
func TestE2EPersistedGrantRemoveAllNoMatchIsNoOp(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewPersistedGrantStore(pool, discardLogger())

	grant := &models.PersistedGrant{
		Key:          "grant-keep",
		Type:         "refresh_token",
		SubjectId:    "subject-keep",
		SessionId:    "session-keep",
		ClientId:     "client-keep",
		CreationTime: fixedNow,
	}
	if err := s.Store(t.Context(), grant); err != nil {
		t.Fatalf("Store: %v", err)
	}

	if err := s.RemoveAll(t.Context(), &storage.PersistedGrantFilter{SubjectId: "absent-subject"}); err != nil {
		t.Fatalf("RemoveAll on non-matching filter: %v", err)
	}
	if err := s.Remove(t.Context(), "absent-key"); err != nil {
		t.Fatalf("Remove on missing key: %v", err)
	}

	all, err := s.GetAll(t.Context(), nil)
	if err != nil {
		t.Fatalf("GetAll after no-op removes: %v", err)
	}
	if len(all) != 1 || all[0].Key != "grant-keep" {
		t.Errorf("grants after no-op removes = %d (%v), want only grant-keep", len(all), all)
	}
}

// assertPersistedGrantExpiresAt asserts the expires_at column for the key;
// want nil asserts a NULL column. The user consent variant is hardcoded to
// its own table and composite natural key, so this variant targets
// persisted_grants with the single-column key.
func assertPersistedGrantExpiresAt(t *testing.T, pool *pgxpool.Pool, key string, want *time.Time) {
	t.Helper()
	var got *time.Time
	err := pool.QueryRow(t.Context(),
		"SELECT expires_at FROM persisted_grants WHERE key = $1", key).Scan(&got)
	if err != nil {
		t.Fatalf("query expires_at for %s: %v", key, err)
	}
	switch {
	case want == nil && got != nil:
		t.Errorf("expires_at for %s = %v, want NULL", key, *got)
	case want != nil && got == nil:
		t.Errorf("expires_at for %s = NULL, want %v", key, want)
	case want != nil && !got.Equal(*want):
		t.Errorf("expires_at for %s = %v, want %v", key, *got, *want)
	}
}
