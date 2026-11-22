// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"strings"
	"testing"
)

// TestStorePushedAuthorizationRequestRejectsNilRequest pins the nil-request
// guard: a nil model fails with an error instead of panicking. The guard
// returns before the payload is encoded or the pool is touched, so a nil
// pool is sufficient; a panic or a nil error here means the guard was lost
// and a nil request would reach the INSERT path.
func TestStorePushedAuthorizationRequestRejectsNilRequest(t *testing.T) {
	s := NewPushedAuthorizationRequestStore(nil, discardLogger())
	err := s.StorePushedAuthorizationRequest(t.Context(), "par-nil-request", nil)
	if err == nil {
		t.Fatal("StorePushedAuthorizationRequest(nil) = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "nil request") {
		t.Errorf("StorePushedAuthorizationRequest(nil) error = %q, want it to mention the nil request", err)
	}
}
