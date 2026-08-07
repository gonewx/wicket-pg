// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test — story 1.3 contract suite factory and fixtures, verified
// end to end against a real PostgreSQL server.
//
// The conformance factory (NewStore) creates a unique schema, a dedicated
// pool pinned to it via search_path, applies migrations inside it, and tears
// the environment down when the test completes. Its package-level tests
// cover the wiring; what they cannot prove is that the whole lifecycle works
// against a real server when the factory is driven from a throwaway
// database: per-call schema isolation, the unquoted-safe schema name shape,
// parallel safety, three-group fixture independence, and teardown that
// leaves nothing behind. These tests pin those behaviors end to end.
//
// Like the story 1.1/1.2 e2e suites, these tests are gated behind
// WICKET_PG_TEST_DATABASE_URL and create a throwaway database per test, so
// the plain test run stays green without a database.
package e2e_test

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/tests/conformance"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaNamePattern matches the factory's documented unique-schema name
// shape: a timestamp plus 16 random hex digits. The restricted lowercase
// charset keeps the name valid as an unquoted SQL identifier, which the
// factory relies on for CREATE/DROP SCHEMA.
var schemaNamePattern = regexp.MustCompile(`^conformance_\d+_[0-9a-f]{16}$`)

// TestE2EFactoryCallsYieldDedicatedSchemas verifies AC-1 end to end: every
// factory call produces a store whose search_path resolves to a unique
// dedicated schema, never the default one, with a name safe to use unquoted.
func TestE2EFactoryCallsYieldDedicatedSchemas(t *testing.T) {
	newScratchPool(t)
	t.Setenv(testDatabaseURLEnv, poolURL(t))

	factory := conformance.NewStore(t, poolConstruct)
	storeA := factory()
	storeB := factory()

	schemaA := currentSchema(t, storeA)
	schemaB := currentSchema(t, storeB)
	if schemaA == schemaB {
		t.Errorf("two factory calls share schema %q", schemaA)
	}
	for _, schema := range []string{schemaA, schemaB} {
		if !schemaNamePattern.MatchString(schema) {
			t.Errorf("schema name %q does not match %s", schema, schemaNamePattern)
		}
		if schema == "public" {
			t.Errorf("store not pinned to dedicated schema: %q", schema)
		}
	}
}

// TestE2EFactoryMigrationsStayInDedicatedSchema verifies AC-2 end to end:
// every migration object lives in the factory's schema inside the scratch
// database and nothing leaks into public.
func TestE2EFactoryMigrationsStayInDedicatedSchema(t *testing.T) {
	admin := newScratchPool(t)
	t.Setenv(testDatabaseURLEnv, poolURL(t))

	factory := conformance.NewStore(t, poolConstruct)
	store := factory()
	schema := currentSchema(t, store)

	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if !relationExistsIn(t, admin, schema, table) {
			t.Errorf("expected table %s in schema %s", table, schema)
		}
		if relationExistsIn(t, admin, "public", table) {
			t.Errorf("table %s leaked into public schema", table)
		}
	}
	for _, index := range expectedIndexes {
		if !indexExistsIn(t, admin, schema, index) {
			t.Errorf("expected index %s in schema %s", index, schema)
		}
	}
}

// TestE2EFactoryIsolatesDataAcrossCalls verifies AC-4 end to end: a row
// written through one store is invisible to a store from another factory
// call — the no-state-leakage contract conformance suites depend on.
func TestE2EFactoryIsolatesDataAcrossCalls(t *testing.T) {
	newScratchPool(t)
	t.Setenv(testDatabaseURLEnv, poolURL(t))

	factory := conformance.NewStore(t, poolConstruct)
	storeA := factory()
	storeB := factory()

	const insert = `INSERT INTO authorization_codes (handle, expires_at, payload)
		VALUES ($1, NULL, '{"version":1}'::jsonb)`
	if _, err := storeA.Exec(t.Context(), insert, "e2e-iso"); err != nil {
		t.Fatalf("insert via store A: %v", err)
	}

	// Visible through store A, invisible through store B: the row must not
	// cross schema boundaries.
	assertRowCount(t, storeA, "e2e-iso", 1)
	assertRowCount(t, storeB, "e2e-iso", 0)
}

// TestE2EFactoryConcurrentCallsDoNotInterfere verifies T3.4 end to end:
// parallel factory invocations produce unique schema names and mutually
// invisible data, the premise t.Parallel suite cases rely on.
func TestE2EFactoryConcurrentCallsDoNotInterfere(t *testing.T) {
	newScratchPool(t)
	t.Setenv(testDatabaseURLEnv, poolURL(t))

	factory := conformance.NewStore(t, poolConstruct)

	var mu sync.Mutex
	seen := make(map[string]bool)

	const workers = 8
	for i := 0; i < workers; i++ {
		t.Run(fmt.Sprintf("worker-%d", i), func(t *testing.T) {
			t.Parallel()
			store := factory()
			schema := currentSchema(t, store)

			mu.Lock()
			if seen[schema] {
				t.Errorf("schema %s reused by concurrent factory call", schema)
			}
			seen[schema] = true
			mu.Unlock()

			handle := fmt.Sprintf("conc-%d", i)
			const insert = `INSERT INTO authorization_codes (handle, expires_at, payload)
				VALUES ($1, NULL, '{"version":1}'::jsonb)`
			if _, err := store.Exec(t.Context(), insert, handle); err != nil {
				t.Fatalf("insert via concurrent store: %v", err)
			}

			// No other worker's row may be visible here.
			var total int
			if err := store.QueryRow(t.Context(),
				"SELECT count(*) FROM authorization_codes").Scan(&total); err != nil {
				t.Fatalf("count rows in concurrent schema: %v", err)
			}
			if total != 1 {
				t.Errorf("schema %s holds %d rows, want 1 (own write only)", schema, total)
			}
		})
	}
}

// TestE2EFixtureGroupsAreIndependent verifies AC-3 end to end: the grant
// family, session, and keymgmt fixture groups each build their own factory,
// and stores from different groups never share a schema or observe each
// other's rows.
func TestE2EFixtureGroupsAreIndependent(t *testing.T) {
	newScratchPool(t)
	t.Setenv(testDatabaseURLEnv, poolURL(t))

	grantFactory := conformance.NewStore(t, poolConstruct)
	sessionFactory := conformance.NewStore(t, poolConstruct)
	keymgmtFactory := conformance.NewStore(t, poolConstruct)

	grantStore := grantFactory()
	sessionStore := sessionFactory()
	keymgmtStore := keymgmtFactory()

	schemas := map[string]string{
		"grant":   currentSchema(t, grantStore),
		"session": currentSchema(t, sessionStore),
		"keymgmt": currentSchema(t, keymgmtStore),
	}
	if schemas["grant"] == schemas["session"] || schemas["grant"] == schemas["keymgmt"] ||
		schemas["session"] == schemas["keymgmt"] {
		t.Errorf("fixture groups share schemas: %v", schemas)
	}

	const insert = `INSERT INTO authorization_codes (handle, expires_at, payload)
		VALUES ($1, NULL, '{"version":1}'::jsonb)`
	if _, err := grantStore.Exec(t.Context(), insert, "group-grant"); err != nil {
		t.Fatalf("insert via grant group store: %v", err)
	}
	assertRowCount(t, sessionStore, "group-grant", 0)
	assertRowCount(t, keymgmtStore, "group-grant", 0)
}

// TestE2EFactoryCleanupDropsSchema verifies the teardown contract end to
// end: once the test completes, the factory's schema no longer exists in the
// scratch database.
func TestE2EFactoryCleanupDropsSchema(t *testing.T) {
	admin := newScratchPool(t)
	t.Setenv(testDatabaseURLEnv, poolURL(t))

	var schemaName string
	t.Cleanup(func() {
		if schemaName == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var exists bool
		if err := admin.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)",
			schemaName).Scan(&exists); err != nil {
			t.Errorf("check schema after cleanup: %v", err)
			return
		}
		if exists {
			t.Errorf("schema %s still exists after factory cleanup", schemaName)
		}
	})

	factory := conformance.NewStore(t, poolConstruct)
	store := factory()
	schemaName = currentSchema(t, store)
}

// poolConstruct returns the pool itself, giving the tests direct access to
// the isolated environment for schema-level assertions.
func poolConstruct(pool *pgxpool.Pool, _ *slog.Logger) *pgxpool.Pool {
	return pool
}

// currentSchema reports the search_path schema the store's pool resolves.
func currentSchema(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var schema string
	if err := pool.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatalf("query current_schema: %v", err)
	}
	return schema
}

// assertRowCount asserts how many rows with handle exist through pool.
func assertRowCount(t *testing.T, pool *pgxpool.Pool, handle string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM authorization_codes WHERE handle = $1",
		handle).Scan(&got); err != nil {
		t.Fatalf("count handle %s: %v", handle, err)
	}
	if got != want {
		t.Errorf("handle %s visible %d times, want %d", handle, got, want)
	}
}
