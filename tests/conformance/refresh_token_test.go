// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
)

// TestRefreshTokenStoreWritesBackVersion pins the caller-side version
// write-back that the conformance suite does not assert directly: after a
// successful StoreRefreshToken the caller's token.Version must be 1 (the
// stored copy's version), and a rejected duplicate store must leave the
// caller's version untouched.
func TestRefreshTokenStoreWritesBackVersion(t *testing.T) {
	newStore := NewStore(t, store.NewRefreshTokenStore)
	s := newStore()

	token := &models.RefreshToken{
		CreationTime: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:     3600,
		ClientId:     "client-a",
		Version:      5, // models.NewRefreshToken default; must be overwritten
	}
	if err := s.StoreRefreshToken(t.Context(), "handle-1", token); err != nil {
		t.Fatalf("store: %v", err)
	}
	if token.Version != 1 {
		t.Errorf("caller Version after store = %d, want 1", token.Version)
	}

	second := &models.RefreshToken{
		CreationTime: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:     3600,
		ClientId:     "client-b",
		Version:      5,
	}
	if err := s.StoreRefreshToken(t.Context(), "handle-1", second); !errors.Is(err, storage.ErrDuplicateHandle) {
		t.Fatalf("duplicate store error = %v, want storage.ErrDuplicateHandle", err)
	}
	if second.Version != 5 {
		t.Errorf("caller Version after rejected duplicate = %d, want 5 (unchanged)", second.Version)
	}
}

// TestRefreshTokenUpdateMissingHandleReturnsNotFound pins the missing
// handle path of the optimistic update: the conformance suite only tests
// version conflicts on existing records, so the ErrNotFound branch of the
// zero-row re-check is pinned here. The caller's version must stay
// untouched on the failure.
func TestRefreshTokenUpdateMissingHandleReturnsNotFound(t *testing.T) {
	newStore := NewStore(t, store.NewRefreshTokenStore)
	s := newStore()

	token := &models.RefreshToken{
		CreationTime: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:     3600,
		ClientId:     "client-a",
		Version:      9,
	}
	err := s.UpdateRefreshToken(t.Context(), "never-stored", token, 1)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("update missing handle error = %v, want storage.ErrNotFound", err)
	}
	if token.Version != 9 {
		t.Errorf("caller Version after missing update = %d, want 9 (unchanged)", token.Version)
	}
}

// TestRefreshTokenUpdateVersionConflictKeepsCaller pins the conflict
// branch of the optimistic update: on ErrVersionConflict the caller's
// token.Version must stay untouched (the suite asserts the stored record
// is unchanged, not the caller object).
func TestRefreshTokenUpdateVersionConflictKeepsCaller(t *testing.T) {
	newStore := NewStore(t, store.NewRefreshTokenStore)
	s := newStore()

	original := &models.RefreshToken{
		CreationTime: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:     3600,
		ClientId:     "client-a",
	}
	if err := s.StoreRefreshToken(t.Context(), "handle-1", original); err != nil {
		t.Fatalf("store: %v", err)
	}

	update := &models.RefreshToken{
		CreationTime: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:     3600,
		ClientId:     "client-b",
		Version:      7,
	}
	err := s.UpdateRefreshToken(t.Context(), "handle-1", update, 99)
	if !errors.Is(err, storage.ErrVersionConflict) {
		t.Fatalf("update with wrong version error = %v, want storage.ErrVersionConflict", err)
	}
	if update.Version != 7 {
		t.Errorf("caller Version after conflict = %d, want 7 (unchanged)", update.Version)
	}
}
