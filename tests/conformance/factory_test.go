// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolFactoryConstruct returns the pool itself, giving the tests direct
// access to the isolated environment for schema-level assertions.
func poolFactoryConstruct(pool *pgxpool.Pool, _ *slog.Logger) *pgxpool.Pool {
	return pool
}

// TestFactoryCallsUseDistinctSchemas verifies AC-1: two factory invocations
// yield stores pinned to different schemas, never the default one.
func TestFactoryCallsUseDistinctSchemas(t *testing.T) {
	factory := NewStore(t, poolFactoryConstruct)
	storeA := factory()
	storeB := factory()

	schemaA := currentSchema(t, storeA)
	schemaB := currentSchema(t, storeB)
	if schemaA == schemaB {
		t.Errorf("two factory calls share schema %q", schemaA)
	}
	if schemaA == "public" || schemaB == "public" {
		t.Errorf("store not pinned to dedicated schema: %q, %q", schemaA, schemaB)
	}
}

// TestFactoryMigrationsLandInDedicatedSchema verifies AC-2: every migration
// object lives in the factory's schema and nothing leaks into public.
func TestFactoryMigrationsLandInDedicatedSchema(t *testing.T) {
	factory := NewStore(t, poolFactoryConstruct)
	store := factory()
	schema := currentSchema(t, store)

	admin := newAdminPool(t)
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
		if indexExistsIn(t, admin, "public", index) {
			t.Errorf("index %s leaked into public schema", index)
		}
	}
}

// TestFactoryIsolatesDataAcrossCalls verifies AC-4: a row written through
// one store is invisible to a store from another factory call.
func TestFactoryIsolatesDataAcrossCalls(t *testing.T) {
	factory := NewStore(t, poolFactoryConstruct)
	storeA := factory()
	storeB := factory()
	schemaA := currentSchema(t, storeA)
	schemaB := currentSchema(t, storeB)

	const insert = `INSERT INTO authorization_codes (handle, expires_at, payload)
		VALUES ($1, NULL, '{"version":1}'::jsonb)`
	if _, err := storeA.Exec(t.Context(), insert, "iso-handle"); err != nil {
		t.Fatalf("insert via store A: %v", err)
	}

	// Visible through store A.
	var countA int
	if err := storeA.QueryRow(t.Context(),
		"SELECT count(*) FROM authorization_codes WHERE handle = $1",
		"iso-handle").Scan(&countA); err != nil {
		t.Fatalf("count via store A: %v", err)
	}
	if countA != 1 {
		t.Errorf("store A sees %d rows, want 1", countA)
	}

	// Invisible through store B: the row must not cross schema boundaries.
	var countB int
	if err := storeB.QueryRow(t.Context(),
		"SELECT count(*) FROM authorization_codes WHERE handle = $1",
		"iso-handle").Scan(&countB); err != nil {
		t.Fatalf("count via store B: %v", err)
	}
	if countB != 0 {
		t.Errorf("row written in schema %s visible in schema %s (count=%d)", schemaA, schemaB, countB)
	}
}

// TestFactoryConcurrentCallsDoNotInterfere verifies T3.4: parallel factory
// invocations produce unique schema names and mutually invisible data.
func TestFactoryConcurrentCallsDoNotInterfere(t *testing.T) {
	factory := NewStore(t, poolFactoryConstruct)

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

// TestFactoryCleanupDropsSchema verifies the teardown contract: once the
// test completes, the factory's schema no longer exists.
func TestFactoryCleanupDropsSchema(t *testing.T) {
	admin := newAdminPool(t)

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

	factory := NewStore(t, poolFactoryConstruct)
	store := factory()
	schemaName = currentSchema(t, store)
}

// newAdminPool connects to the database named by the test URL, skipping when
// the environment variable is unset. The pool is closed by cleanup.
func newAdminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(testDatabaseURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping conformance tests", testDatabaseURLEnv)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	t.Cleanup(pool.Close)
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

func relationExistsIn(t *testing.T, pool *pgxpool.Pool, schema, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)",
		schema, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s in %s: %v", name, schema, err)
	}
	return exists
}

func indexExistsIn(t *testing.T, pool *pgxpool.Pool, schema, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = $2)",
		schema, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %s in %s: %v", name, schema, err)
	}
	return exists
}

// businessTables lists every table the initial migration creates, mirroring
// the story 1.1 e2e assertion list (own copy: test helpers never import
// across packages).
var businessTables = []string{
	"authorization_codes",
	"refresh_tokens",
	"reference_tokens",
	"user_consents",
	"persisted_grants",
	"device_codes",
	"backchannel_auth_requests",
	"sessions",
	"key_records",
}

var expectedIndexes = []string{
	"idx_authorization_codes_expires_at",
	"idx_refresh_tokens_expires_at",
	"idx_reference_tokens_expires_at",
	"idx_user_consents_expires_at",
	"idx_persisted_grants_expires_at",
	"idx_device_codes_expires_at",
	"idx_device_codes_user_code",
	"idx_backchannel_auth_requests_expires_at",
	"idx_sessions_expires",
	"idx_sessions_subject_id",
	"idx_persisted_grants_subject_id",
	"idx_persisted_grants_session_id",
	"idx_persisted_grants_client_id",
	"idx_persisted_grants_type",
	"idx_key_records_public_id_unique",
}
