// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage/models"
)

// TestAuthorizationCodeGetReturnsIndependentCopy verifies AC-3 on a real
// PostgreSQL server: mutating the value returned by GetAuthorizationCode
// must not affect the stored record, which a later read observes unchanged.
// The conformance suite does not assert this directly, so it is pinned here
// end to end.
func TestAuthorizationCodeGetReturnsIndependentCopy(t *testing.T) {
	newStore := NewStore(t, store.NewAuthorizationCodeStore)
	s := newStore()

	code := &models.AuthorizationCode{
		CreationTime:    time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:        300,
		ClientId:        "client-a",
		IsOpenId:        true,
		RequestedScopes: []string{"openid", "profile"},
		Properties:      map[string]string{"k": "v"},
	}
	if err := s.StoreAuthorizationCode(t.Context(), "handle-1", code); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, err := s.GetAuthorizationCode(t.Context(), "handle-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.ClientId = "mutated"
	got.RequestedScopes[0] = "mutated"
	got.Properties["k"] = "mutated"

	again, err := s.GetAuthorizationCode(t.Context(), "handle-1")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if again.ClientId != "client-a" {
		t.Errorf("ClientId = %q, want %q (stored record mutated)", again.ClientId, "client-a")
	}
	if len(again.RequestedScopes) != 2 || again.RequestedScopes[0] != "openid" {
		t.Errorf("RequestedScopes = %v, want [openid profile] (slice shared)", again.RequestedScopes)
	}
	if v, ok := again.Properties["k"]; !ok || v != "v" {
		t.Errorf("Properties[k] = %q, want %q (map shared)", v, "v")
	}
}
