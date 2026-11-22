// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"testing"

	"github.com/gonewx/wicket/storage/models"
)

// TestClientRegistrationWritesRejectInvalidRegistration pins the input
// guards of RegisterClient and UpdateClientRegistration: a nil registration,
// a registration without a client and a client without an id all fail with
// an error instead of panicking. The guards return before anything is
// encoded or the pool is touched, so a nil pool is sufficient; a panic or a
// nil error here means a guard was lost and the input would reach the SQL
// path.
func TestClientRegistrationWritesRejectInvalidRegistration(t *testing.T) {
	s := NewClientRegistrationStore(nil, discardLogger())
	cases := []struct {
		name         string
		registration *models.ClientRegistration
	}{
		{"NilRegistration", nil},
		{"NilClient", &models.ClientRegistration{}},
		{"EmptyClientId", &models.ClientRegistration{Client: models.NewClient()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if revision, err := s.RegisterClient(t.Context(), tc.registration); err == nil {
				t.Errorf("RegisterClient = %d, nil error; want an error", revision)
			}
			if revision, err := s.UpdateClientRegistration(t.Context(), tc.registration, 1); err == nil {
				t.Errorf("UpdateClientRegistration = %d, nil error; want an error", revision)
			}
		})
	}
}
