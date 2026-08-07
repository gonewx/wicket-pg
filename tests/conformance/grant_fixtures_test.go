// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"log/slog"
	"testing"

	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/storagetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Grant-family suite fixtures.
//
// The seven grant storage port suites each run against their own factory
// built from NewStore with the matching store constructor, so every suite
// case gets a brand-new empty store in its own schema:
//
//	storagetest.RunRefreshTokenStoreSuite(t, factory, opts...)
//	storagetest.RunReferenceTokenStoreSuite(t, factory, opts...)
//	storagetest.RunUserConsentStoreSuite(t, factory, opts...)
//	storagetest.RunPersistedGrantStoreSuite(t, factory, opts...)
//	storagetest.RunDeviceFlowStoreSuite(t, factory, opts...)
//	storagetest.RunBackchannelAuthenticationRequestStoreSuite(t, factory, opts...)
//
// Each factory is independent: no schema name, pool, or constructor state is
// shared between the seven entries (AC-3, AD-9). The remaining entry points
// are wired by stories 1.6-1.10 as each store adapter lands; this file
// currently declares those access points, not their constructors.

// TestAuthorizationCodeStoreSuite runs the authorization code conformance
// suite (8 MUST cases plus the MAY credential case, enabled via WithMay)
// against the pgx-backed adapter on a real PostgreSQL server. The suite is
// skipped when WICKET_PG_TEST_DATABASE_URL is unset.
func TestAuthorizationCodeStoreSuite(t *testing.T) {
	storagetest.RunAuthorizationCodeStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.AuthorizationCodeStore {
			return store.NewAuthorizationCodeStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestRefreshTokenStoreSuite runs the refresh token conformance suite (10
// MUST cases plus the 2 MAY cases — rotation replay defense and the
// credential — enabled via WithMay) against the pgx-backed adapter on a
// real PostgreSQL server. The suite is skipped when
// WICKET_PG_TEST_DATABASE_URL is unset.
func TestRefreshTokenStoreSuite(t *testing.T) {
	storagetest.RunRefreshTokenStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.RefreshTokenStore {
			return store.NewRefreshTokenStore(pool, logger)
		}),
		storagetest.WithMay(true))
}
