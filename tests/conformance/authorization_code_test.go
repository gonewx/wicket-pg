// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/claims"
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

// TestAuthorizationCodeSubjectClaimsRoundTrip pins the principal round trip:
// a code whose Subject carries claims must come back with the same claims.
// encoding/json silently drops unexported struct fields, so a codec that
// marshals ClaimsIdentity through its struct shape alone loses every claim;
// the wicket claims model therefore exposes JSON round trip and this test
// guards the adapter against regressing to a lossy codec.
func TestAuthorizationCodeSubjectClaimsRoundTrip(t *testing.T) {
	newStore := NewStore(t, store.NewAuthorizationCodeStore)
	s := newStore()

	code := &models.AuthorizationCode{
		CreationTime:    time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Lifetime:        300,
		ClientId:        "client-a",
		IsOpenId:        true,
		RequestedScopes: []string{"openid", "profile"},
		Subject: claims.NewClaimsPrincipal(
			claims.WithClaimsPrincipalIdentity(
				claims.NewClaimsIdentity(
					claims.WithAuthenticationType("wicket"),
					claims.WithClaims([]*claims.Claim{
						claims.NewClaim(claims.WithClaimType("sub"), claims.WithClaimValue("1")),
						claims.NewClaim(claims.WithClaimType("name"), claims.WithClaimValue("Alice Smith")),
					}),
				),
			),
		),
	}
	if err := s.StoreAuthorizationCode(t.Context(), "handle-subject", code); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, err := s.GetAuthorizationCode(t.Context(), "handle-subject")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject == nil {
		t.Fatal("Subject = nil, want the stored principal")
	}
	if len(got.Subject.Identities) != 1 {
		t.Fatalf("identities = %d, want 1", len(got.Subject.Identities))
	}
	identity := got.Subject.Identities[0]
	if identity.AuthenticationType() != "wicket" {
		t.Errorf("AuthenticationType() = %q, want %q", identity.AuthenticationType(), "wicket")
	}
	if len(identity.Claims()) != 2 {
		t.Fatalf("claims = %d, want 2 (claims lost in round trip)", len(identity.Claims()))
	}
	if sub := identity.FindFirst("sub"); sub == nil || sub.Value != "1" {
		t.Errorf("sub claim lost in round trip: %+v", sub)
	}
	if name := identity.FindFirst("name"); name == nil || name.Value != "Alice Smith" {
		t.Errorf("name claim lost in round trip: %+v", name)
	}
}
