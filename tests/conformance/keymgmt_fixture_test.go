// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"log/slog"
	"testing"

	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/keymgmt"
	"github.com/gonewx/wicket/keymgmt/keymgmttest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Key-record suite fixture.
//
// The key management record store port suite runs against its own factory
// built from NewStore with the key-record store constructor, kept strictly
// separate from the grant-family and session fixtures: no schema name, pool,
// or constructor state is shared across the groups (AC-3, AD-9).
//
// Unlike the session fixture, whose LazyExpiryReclaimsOnRead MAY case is not
// implementable, both keymgmt MAY cases (ConcurrentUpdateSingleWinner and
// ConformsToCredential) are implementable, so the fixture enables them with
// WithMay(true) (AC-11).

// TestKeyRecordStoreSuite runs the key record conformance suite (16 MUST
// cases plus the 2 MAY cases, enabled via WithMay) against the pgx-backed
// adapter on a real PostgreSQL server. The suite is skipped when
// WICKET_PG_TEST_DATABASE_URL is unset.
func TestKeyRecordStoreSuite(t *testing.T) {
	keymgmttest.RunRecordStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) keymgmt.RecordStore {
			return store.NewKeyRecordStore(pool, logger)
		}),
		keymgmttest.WithMay(true))
}
