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
// The nine grant storage port suites each run against their own factory
// built from NewStore with the matching store constructor, so every suite
// case gets a brand-new empty store in its own schema:
//
//	storagetest.RunAuthorizationCodeStoreSuite(t, factory, opts...)
//	storagetest.RunRefreshTokenStoreSuite(t, factory, opts...)
//	storagetest.RunReferenceTokenStoreSuite(t, factory, opts...)
//	storagetest.RunUserConsentStoreSuite(t, factory, opts...)
//	storagetest.RunPersistedGrantStoreSuite(t, factory, opts...)
//	storagetest.RunDeviceFlowStoreSuite(t, factory, opts...)
//	storagetest.RunBackchannelAuthenticationRequestStoreSuite(t, factory, opts...)
//	storagetest.RunPushedAuthorizationRequestStoreSuite(t, factory, opts...)
//	storagetest.RunClientRegistrationStoreSuite(t, factory, opts...)
//
// Each factory is independent: no schema name, pool, or constructor state is
// shared between the nine entries (AC-3, AD-9).
//
// The MUST/MAY case counts in the comments below are those of storagetest
// SuiteVersion 4.0.0 (wicket v0.10.0); re-count them on every suite bump.

// TestAuthorizationCodeStoreSuite runs the authorization code conformance
// suite (17 MUST cases plus the MAY credential case, enabled via WithMay)
// against the pgx-backed adapter on a real PostgreSQL server. The suite is
// skipped when WICKET_PG_TEST_DATABASE_URL is unset.
func TestAuthorizationCodeStoreSuite(t *testing.T) {
	storagetest.RunAuthorizationCodeStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.AuthorizationCodeStore {
			return store.NewAuthorizationCodeStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestRefreshTokenStoreSuite runs the refresh token conformance suite (19
// MUST cases plus the 3 MAY cases — rotation replay defense, the lifetime
// update round trip and the credential — enabled via WithMay) against the
// pgx-backed adapter on a real PostgreSQL server. The suite is skipped when
// WICKET_PG_TEST_DATABASE_URL is unset.
func TestRefreshTokenStoreSuite(t *testing.T) {
	storagetest.RunRefreshTokenStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.RefreshTokenStore {
			return store.NewRefreshTokenStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestReferenceTokenStoreSuite runs the reference token conformance suite
// (14 MUST cases plus the MAY credential case, enabled via WithMay) against
// the pgx-backed adapter on a real PostgreSQL server. The suite is skipped
// when WICKET_PG_TEST_DATABASE_URL is unset.
func TestReferenceTokenStoreSuite(t *testing.T) {
	storagetest.RunReferenceTokenStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.ReferenceTokenStore {
			return store.NewReferenceTokenStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestUserConsentStoreSuite runs the user consent conformance suite (13 MUST
// cases plus the MAY credential case, enabled via WithMay) against the
// pgx-backed adapter on a real PostgreSQL server. The suite is skipped when
// WICKET_PG_TEST_DATABASE_URL is unset.
func TestUserConsentStoreSuite(t *testing.T) {
	storagetest.RunUserConsentStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.UserConsentStore {
			return store.NewUserConsentStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestPersistedGrantStoreSuite runs the persisted grant conformance suite
// (14 MUST cases plus the 2 MAY cases — the combined filter and the
// credential — enabled via WithMay) against the pgx-backed adapter on a
// real PostgreSQL server. The suite is skipped when
// WICKET_PG_TEST_DATABASE_URL is unset.
func TestPersistedGrantStoreSuite(t *testing.T) {
	storagetest.RunPersistedGrantStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.PersistedGrantStore {
			return store.NewPersistedGrantStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestDeviceFlowStoreSuite runs the device flow conformance suite (14 MUST
// cases plus the 2 MAY cases — the update read-back and the credential —
// enabled via WithMay) against the pgx-backed adapter on a real PostgreSQL
// server. The suite is skipped when WICKET_PG_TEST_DATABASE_URL is unset.
func TestDeviceFlowStoreSuite(t *testing.T) {
	storagetest.RunDeviceFlowStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.DeviceFlowStore {
			return store.NewDeviceFlowStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestBackchannelAuthenticationRequestStoreSuite runs the backchannel
// authentication request conformance suite (10 MUST cases plus the MAY
// credential case, enabled via WithMay) against the pgx-backed adapter on a
// real PostgreSQL server. The suite is skipped when
// WICKET_PG_TEST_DATABASE_URL is unset.
func TestBackchannelAuthenticationRequestStoreSuite(t *testing.T) {
	storagetest.RunBackchannelAuthenticationRequestStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.BackchannelAuthenticationRequestStore {
			return store.NewBackchannelAuthenticationRequestStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestPushedAuthorizationRequestStoreSuite runs the pushed authorization
// request conformance suite (15 MUST cases plus the 2 MAY cases
// ReturnedCopyIsolated and ConformsToCredential, enabled via WithMay)
// against the pgx-backed adapter on a real PostgreSQL server. The suite is
// skipped when WICKET_PG_TEST_DATABASE_URL is unset.
func TestPushedAuthorizationRequestStoreSuite(t *testing.T) {
	storagetest.RunPushedAuthorizationRequestStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.PushedAuthorizationRequestStore {
			return store.NewPushedAuthorizationRequestStore(pool, logger)
		}),
		storagetest.WithMay(true))
}

// TestClientRegistrationStoreSuite runs the client registration conformance
// suite (15 MUST cases, the origin lookup among them, plus the 2 MAY cases
// ReturnedRecordIsolated and ConformsToCredential, enabled via WithMay)
// against the pgx-backed adapter on a real PostgreSQL server. The same store
// value serves storage.ClientOriginLookup, which the suite asserts. The
// suite is skipped when WICKET_PG_TEST_DATABASE_URL is unset.
func TestClientRegistrationStoreSuite(t *testing.T) {
	storagetest.RunClientRegistrationStoreSuite(t,
		NewStore(t, func(pool *pgxpool.Pool, logger *slog.Logger) storage.ClientRegistrationStore {
			return store.NewClientRegistrationStore(pool, logger)
		}),
		storagetest.WithMay(true))
}
