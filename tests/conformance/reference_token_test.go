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

// TestReferenceTokenRemoveMissingHandleIsNoOp pins the write-path zero-row
// semantics that the conformance suite does not assert directly: removing
// a handle that was never stored succeeds instead of failing, and a
// subsequent read still reports storage.ErrNotFound.
func TestReferenceTokenRemoveMissingHandleIsNoOp(t *testing.T) {
	newStore := NewStore(t, store.NewReferenceTokenStore)
	s := newStore()

	if err := s.RemoveReferenceToken(t.Context(), "never-stored"); err != nil {
		t.Fatalf("RemoveReferenceToken on missing handle: %v", err)
	}
	if got, err := s.GetReferenceToken(t.Context(), "never-stored"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get after no-op remove = %v, %v; want nil, ErrNotFound", got, err)
	}
}

// TestReferenceTokenRemoveTokensEmptyMatchSetIsNoOp pins the empty-match
// path of the batch revocation: revoking a subject/client pair that
// matches nothing returns nil and leaves every stored token alive. The
// suite only covers the non-empty paths.
func TestReferenceTokenRemoveTokensEmptyMatchSetIsNoOp(t *testing.T) {
	newStore := NewStore(t, store.NewReferenceTokenStore)
	s := newStore()

	stored := []struct {
		handle string
		client string
	}{
		{"h1", "client-a"},
		{"h2", "client-b"},
	}
	for _, tc := range stored {
		if err := s.StoreReferenceToken(t.Context(), tc.handle, &models.Token{
			CreationTime: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
			Lifetime:     3600,
			ClientId:     tc.client,
		}); err != nil {
			t.Fatalf("store %s: %v", tc.handle, err)
		}
	}

	if err := s.RemoveReferenceTokens(t.Context(), "known-subject-no-match", "client-a"); err != nil {
		t.Fatalf("RemoveReferenceTokens with empty match set: %v", err)
	}
	for _, tc := range stored {
		if got, err := s.GetReferenceToken(t.Context(), tc.handle); err != nil || got == nil {
			t.Errorf("Get(%s) after no-op revocation = %v, %v; want the stored token", tc.handle, got, err)
		}
	}
}
