// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the story 1.14 compliance credentials at the
// assembled-host layer: all nine stores constructed against one real,
// migrated schema (the host assembly scenario) report their suite version
// without panicking, each call returns the value of the suite package's
// exported constant, and repeated calls are stable. The nil-pool unit test
// in store/conforms_to_test.go covers the same table without a database;
// this suite proves the credential survives real wiring, complements the
// single-store direct checks (session_e2e_test.go, key_records_e2e_test.go),
// and is gated behind WICKET_PG_TEST_DATABASE_URL like every other e2e
// test here.
package e2e_test

import (
	"testing"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/keymgmt/keymgmttest"
	"github.com/gonewx/wicket/session/sessiontest"
	"github.com/gonewx/wicket/storage/storagetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// conformsToCredential is one row of the unified compliance-credential
// table. The factory field's interface type is the compile-time assertion
// of the single-value signature (AC-3): a three-value aggregation such as
// (major, minor, patch) would not satisfy ConformsTo() string and this test
// would not compile.
type conformsToCredential struct {
	name    string
	factory func(pool *pgxpool.Pool) interface{ ConformsTo() string }
	want    string
}

// TestE2EAllStoresConformToSuiteVersions drives AC-1/AC-2/AC-4 end to end:
// against one scratch database with the full migration applied, every store
// constructor is invoked exactly as a host would wire it, and each store
// reports its suite package's exported constant (AC-2) without panicking
// (AC-4). The second call asserts the credential is stable — ConformsTo
// performs no stateful validation that could change a later answer.
func TestE2EAllStoresConformToSuiteVersions(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	cases := []conformsToCredential{
		{"AuthorizationCodeStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewAuthorizationCodeStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"RefreshTokenStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewRefreshTokenStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"ReferenceTokenStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewReferenceTokenStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"UserConsentStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewUserConsentStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"PersistedGrantStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewPersistedGrantStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"DeviceFlowStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewDeviceFlowStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"BackchannelAuthenticationRequestStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewBackchannelAuthenticationRequestStore(p, discardLogger())
		}, storagetest.SuiteVersion},
		{"SessionStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewSessionStore(p, discardLogger())
		}, sessiontest.SuiteVersion},
		{"KeyRecordStore", func(p *pgxpool.Pool) interface{ ConformsTo() string } {
			return store.NewKeyRecordStore(p, discardLogger())
		}, keymgmttest.SuiteVersion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cred := tc.factory(pool)
			first := cred.ConformsTo()
			if first != tc.want {
				t.Errorf("ConformsTo() = %q, want %q", first, tc.want)
			}
			if again := cred.ConformsTo(); again != first {
				t.Errorf("ConformsTo() unstable: first %q, second %q", first, again)
			}
		})
	}
}

// TestE2EConformsToNeedsNoLogger pins the logger contract at the assembly
// layer: stores accept a nil logger and ConformsTo still returns the suite
// version without panicking — a host that wires the credential check before
// logger setup must not crash. The suite version is the exported constant,
// never a literal (AC-2).
func TestE2EConformsToNeedsNoLogger(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	cred := store.NewKeyRecordStore(pool, nil)
	if got := cred.ConformsTo(); got != keymgmttest.SuiteVersion {
		t.Errorf("ConformsTo() with nil logger = %q, want %q", got, keymgmttest.SuiteVersion)
	}
}
