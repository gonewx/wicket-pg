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
// The suite runs without WithMay. The MAY case LazyExpiryReclaimsOnRead is
// not implementable here: the adapter constructor takes no clock (AD-1),
// time.Now() inside the adapter would break the MUST case
// ReclaimBoundaryStrictlyBefore (whose fixed time base predates the real
// clock), and a database clock would violate NFR-1.6/AD-5. The three
// implementable MAY semantics (concurrent appends, concurrent create
// single-winner, ConformsTo) are verified equivalently by the e2e suite;
// see AC-12 in story 1.11.
func TestSessionStoreSuite(t *testing.T) {
	sessiontest.RunSessionStoreSuite(t, NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) session.Store {
		return store.NewSessionStore(pool, logger)
	}))
}
