// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"context"
	"testing"
)

// TestScopedRemovalEmptyArgumentIssuesNoSQL pins the empty-argument short
// circuit of every removal by client or by session: each store is built on a
// nil pool, so any attempt to reach the database would panic. Returning
// (0, nil) without panicking is the observable evidence that no SQL is issued
// for an empty argument.
func TestScopedRemovalEmptyArgumentIssuesNoSQL(t *testing.T) {
	codes := NewAuthorizationCodeStore(nil, discardLogger())
	refresh := NewRefreshTokenStore(nil, discardLogger())
	reference := NewReferenceTokenStore(nil, discardLogger())
	consents := NewUserConsentStore(nil, discardLogger())
	devices := NewDeviceFlowStore(nil, discardLogger())

	cases := []struct {
		name   string
		remove func(ctx context.Context, arg string) (int, error)
	}{
		{"RemoveAuthorizationCodesByClient", codes.RemoveAuthorizationCodesByClient},
		{"RemoveAuthorizationCodesBySession", codes.RemoveAuthorizationCodesBySession},
		{"RemoveRefreshTokensByClient", refresh.RemoveRefreshTokensByClient},
		{"RemoveRefreshTokensBySession", refresh.RemoveRefreshTokensBySession},
		{"RemoveReferenceTokensByClient", reference.RemoveReferenceTokensByClient},
		{"RemoveReferenceTokensBySession", reference.RemoveReferenceTokensBySession},
		{"RemoveUserConsentsByClient", consents.RemoveUserConsentsByClient},
		{"RemoveDeviceCodesBySession", devices.RemoveDeviceCodesBySession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s(\"\") reached the pool: %v", tc.name, r)
				}
			}()
			n, err := tc.remove(t.Context(), "")
			if n != 0 || err != nil {
				t.Errorf("%s(\"\") = (%d, %v), want (0, nil)", tc.name, n, err)
			}
		})
	}
}
