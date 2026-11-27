// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"log/slog"
	"testing"

	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/session/sessiontest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSessionStoreSuite runs the session store port suite against its own
// factory built from NewStore with the session store constructor, kept
// strictly separate from the grant-family and key-record fixtures: no
// schema name, pool, or constructor state is shared across the groups
// (AC-3, AD-9).
//
// The suite runs with WithMay, so the four MAY concurrency cases
// (concurrent appends, concurrent create single-winner, and the
// RenewSession / TouchSession races against AddClientID) are exercised
// against real PostgreSQL. The MAY case LazyExpiryReclaimsOnRead, which
// kept the suite on MUST-only before, was removed in suite 5.0.0 because it
// contradicts the MUST rule that read paths do not judge expiry.
func TestSessionStoreSuite(t *testing.T) {
	sessiontest.RunSessionStoreSuite(t, NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) session.Store {
		return store.NewSessionStore(pool, logger)
	}), sessiontest.WithMay(true))
}
