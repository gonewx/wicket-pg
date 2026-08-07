// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the key record store's behavior against the real
// schema at the level the conformance suite cannot reach: the public_id /
// phase / version columns written by Create and Update (the partial unique
// index and the version guard only work when the columns mirror the model),
// the partial unique index scope (the WHERE phase <> 'retired' predicate in
// pg_indexes), the missing-delete sentinel, the double-base64 lossless round
// trip of non-UTF-8 key material plus independent copies, and the
// ConformsTo credential. The suite is gated behind
// WICKET_PG_TEST_DATABASE_URL so the plain test run stays green without a
// database.
package e2e_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/keymgmt"
	"github.com/gonewx/wicket/keymgmt/keymgmttest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestE2EKeyRecordCreateColumns asserts the three real columns written by
// Create: public_id, phase, and version land exactly as the model says
// (version 1), so the partial unique index and the version guard see the
// values they depend on.
func TestE2EKeyRecordCreateColumns(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewKeyRecordStore(pool, discardLogger())

	rec := newKeyRecordE2E("k-create-1", "kid-create-1")
	if err := s.Create(t.Context(), rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Version != 1 {
		t.Errorf("caller Version after Create = %d, want 1", rec.Version)
	}
	assertKeyRecordColumns(t, pool, "k-create-1", "kid-create-1", "active", 1)
}

// TestE2EKeyRecordUpdateColumns asserts the conditional update refreshes the
// real columns together: a successful update bumps the version column to 2
// and rewrites public_id and phase, while a version conflict leaves every
// column untouched.
func TestE2EKeyRecordUpdateColumns(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewKeyRecordStore(pool, discardLogger())

	rec := newKeyRecordE2E("k-upd-1", "kid-upd-1")
	if err := s.Create(t.Context(), rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A stale writer must not touch the stored row.
	stale := newKeyRecordE2E("k-upd-1", "kid-upd-9")
	stale.Phase = keymgmt.PhaseRetained
	if err := s.Update(t.Context(), stale, 99); !errors.Is(err, keymgmt.ErrVersionConflict) {
		t.Fatalf("Update with wrong version = %v, want ErrVersionConflict", err)
	}
	assertKeyRecordColumns(t, pool, "k-upd-1", "kid-upd-1", "active", 1)

	// A matching version updates the columns and writes back version 2.
	rec.PublicID = "kid-upd-2"
	rec.Phase = keymgmt.PhaseRetained
	rec.Priority = 7
	if err := s.Update(t.Context(), rec, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Version != 2 {
		t.Errorf("caller Version after Update = %d, want 2", rec.Version)
	}
	assertKeyRecordColumns(t, pool, "k-upd-1", "kid-upd-2", "retained", 2)
}

// TestE2EKeyRecordPartialUniqueIndexScope pins the partial unique index
// behavior through the store: a non-retired collision fails with
// ErrDuplicateKey, a retired record does not block reuse of its public id,
// and the index predicate in pg_indexes is exactly WHERE phase <> 'retired'.
func TestE2EKeyRecordPartialUniqueIndexScope(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewKeyRecordStore(pool, discardLogger())

	if err := s.Create(t.Context(), newKeyRecordE2E("k-dup-1", "kid-dup-1")); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	// A second non-retired record with the same public id must conflict.
	second := newKeyRecordE2E("k-dup-2", "kid-dup-1")
	if err := s.Create(t.Context(), second); !errors.Is(err, keymgmt.ErrDuplicateKey) {
		t.Fatalf("Create with duplicate non-retired public_id = %v, want ErrDuplicateKey", err)
	}

	// A retired record may reuse the public id.
	retired := newKeyRecordE2E("k-ret-1", "kid-ret-1")
	retired.Phase = keymgmt.PhaseRetired
	if err := s.Create(t.Context(), retired); err != nil {
		t.Fatalf("Create retired: %v", err)
	}
	reuse := newKeyRecordE2E("k-ret-2", "kid-ret-1")
	if err := s.Create(t.Context(), reuse); err != nil {
		t.Errorf("Create reusing retired public_id = %v, want success", err)
	}

	// The index predicate must be the retired exclusion.
	var indexdef string
	err := pool.QueryRow(t.Context(),
		"SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_key_records_public_id_unique'").Scan(&indexdef)
	if err != nil {
		t.Fatalf("query index definition: %v", err)
	}
	if !bytes.Contains([]byte(indexdef), []byte("phase <> 'retired'")) {
		t.Errorf("index predicate = %q, want it to contain phase <> 'retired'", indexdef)
	}
}

// TestE2EKeyRecordDeleteMissingSentinel pins the store's unique delete
// semantic: deleting a missing handle fails with ErrRecordNotFound (not a
// no-op), and deleting an existing record makes the subsequent Get fail
// with the same sentinel.
func TestE2EKeyRecordDeleteMissingSentinel(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewKeyRecordStore(pool, discardLogger())

	if err := s.Delete(t.Context(), "missing"); !errors.Is(err, keymgmt.ErrRecordNotFound) {
		t.Fatalf("Delete(missing) = %v, want ErrRecordNotFound", err)
	}

	if err := s.Create(t.Context(), newKeyRecordE2E("k-del-1", "kid-del-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Delete(t.Context(), "k-del-1"); err != nil {
		t.Fatalf("Delete existing: %v", err)
	}
	if _, err := s.Get(t.Context(), "k-del-1"); !errors.Is(err, keymgmt.ErrRecordNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrRecordNotFound", err)
	}
}

// TestE2EKeyRecordMaterialRoundTrip pins the double-base64 lossless round
// trip of key material with non-UTF-8 bytes and the independent-copy
// contract: mutating a returned record's fields or material slices must not
// affect the store, because every read decodes the payload afresh.
func TestE2EKeyRecordMaterialRoundTrip(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.NewKeyRecordStore(pool, discardLogger())

	rec := newKeyRecordE2E("k-mat-1", "kid-mat-1")
	rec.PublicMaterial = []byte{0x00, 0xFF, 0x80}
	rec.ProtectedPrivateMaterial = []byte{0x00, 0xFE, 0x81, 0x00}
	if err := s.Create(t.Context(), rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(t.Context(), "k-mat-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.PublicMaterial, rec.PublicMaterial) {
		t.Errorf("PublicMaterial round trip = %v, want %v", got.PublicMaterial, rec.PublicMaterial)
	}
	if !bytes.Equal(got.ProtectedPrivateMaterial, rec.ProtectedPrivateMaterial) {
		t.Errorf("ProtectedPrivateMaterial round trip = %v, want %v", got.ProtectedPrivateMaterial, rec.ProtectedPrivateMaterial)
	}

	// Mutating the returned record must not reach the store.
	got.PublicID = "mutated"
	got.PublicMaterial[0] = 'X'
	got.ProtectedPrivateMaterial[0] = 'Y'

	again, err := s.Get(t.Context(), "k-mat-1")
	if err != nil {
		t.Fatalf("Get after mutation: %v", err)
	}
	if again.PublicID != "kid-mat-1" {
		t.Errorf("PublicID after mutation = %q, want kid-mat-1", again.PublicID)
	}
	if !bytes.Equal(again.PublicMaterial, rec.PublicMaterial) {
		t.Errorf("PublicMaterial after mutation = %v, want %v", again.PublicMaterial, rec.PublicMaterial)
	}
	if !bytes.Equal(again.ProtectedPrivateMaterial, rec.ProtectedPrivateMaterial) {
		t.Errorf("ProtectedPrivateMaterial after mutation = %v, want %v", again.ProtectedPrivateMaterial, rec.ProtectedPrivateMaterial)
	}

	listed, err := s.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List returned %d records, want 1", len(listed))
	}
	listed[0].PublicMaterial[0] = 'Z'
	again, err = s.Get(t.Context(), "k-mat-1")
	if err != nil {
		t.Fatalf("Get after List mutation: %v", err)
	}
	if !bytes.Equal(again.PublicMaterial, rec.PublicMaterial) {
		t.Errorf("PublicMaterial after List mutation = %v, want %v", again.PublicMaterial, rec.PublicMaterial)
	}
}

// TestE2EKeyRecordConformsTo is the e2e double-check of the MAY case
// ConformsToCredential: the adapter reports the keymgmt suite version.
func TestE2EKeyRecordConformsTo(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := store.NewKeyRecordStore(pool, discardLogger()).ConformsTo(); got != keymgmttest.SuiteVersion {
		t.Errorf("ConformsTo() = %q, want %q", got, keymgmttest.SuiteVersion)
	}
}

// newKeyRecordE2E builds a key record with the suite's fixed time base and
// the defaults shared by the conformance fixture.
func newKeyRecordE2E(handle, publicID string) *keymgmt.Record {
	return &keymgmt.Record{
		Handle:                   handle,
		PublicID:                 publicID,
		Purpose:                  keymgmt.PurposeTokenSigning,
		Algorithm:                "RS256",
		Phase:                    keymgmt.PhaseActive,
		CreatedAt:                time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		PublicMaterial:           []byte("public-bytes"),
		ProtectedPrivateMaterial: []byte("private-bytes"),
	}
}

// assertKeyRecordColumns asserts the real columns of the key record row.
func assertKeyRecordColumns(t *testing.T, pool *pgxpool.Pool, handle, wantPublicID, wantPhase string, wantVersion int64) {
	t.Helper()
	var publicID, phase string
	var version int64
	err := pool.QueryRow(t.Context(),
		"SELECT public_id, phase, version FROM key_records WHERE handle = $1", handle).
		Scan(&publicID, &phase, &version)
	if err != nil {
		t.Fatalf("query columns for %s: %v", handle, err)
	}
	if publicID != wantPublicID {
		t.Errorf("public_id for %s = %q, want %q", handle, publicID, wantPublicID)
	}
	if phase != wantPhase {
		t.Errorf("phase for %s = %q, want %q", handle, phase, wantPhase)
	}
	if version != wantVersion {
		t.Errorf("version for %s = %d, want %d", handle, version, wantVersion)
	}
}
