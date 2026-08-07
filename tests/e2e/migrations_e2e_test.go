// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test exercises the migration package end to end against a real
// PostgreSQL server: the full Up/Down lifecycle, search_path-relative DDL,
// the schema contract the store adapters will rely on, and error behavior.
// The suite is gated behind WICKET_PG_TEST_DATABASE_URL (an admin database
// whose role can CREATE DATABASE) so the plain test run stays green without
// a database.
package e2e_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testDatabaseURLEnv = "WICKET_PG_TEST_DATABASE_URL"

// businessTables lists every table the initial migration must create, in
// story 1.1 order: bookkeeping first, then the nine business tables.
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
	"idx_persisted_grants_subject_id",
	"idx_persisted_grants_session_id",
	"idx_persisted_grants_client_id",
	"idx_persisted_grants_type",
	"idx_key_records_public_id_unique",
}

// TestE2EMigrationLifecycle walks the full acceptance path on a throwaway
// database: one Up creates every object and records the version, a second
// Up is an idempotent no-op, Down removes everything in mirror order, and a
// final Up rebuilds the schema from scratch.
func TestE2EMigrationLifecycle(t *testing.T) {
	pool := newScratchPool(t)

	// AC-1: single Up on an empty database.
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	assertObjectsPresent(t, pool)
	assertAppliedVersions(t, pool, []string{"000001", "000002"})

	// AC-1: second Up must not error and must not duplicate objects.
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	assertObjectsPresent(t, pool)
	assertAppliedVersions(t, pool, []string{"000001", "000002"})

	// AC-3: Down rolls everything back and clears the bookkeeping.
	if err := migrations.Down(t.Context(), pool); err != nil {
		t.Fatalf("Down: %v", err)
	}
	assertObjectsAbsent(t, pool)

	// The database can be rebuilt afterwards.
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}
	assertObjectsPresent(t, pool)
	assertAppliedVersions(t, pool, []string{"000001", "000002"})
}

// TestE2EMigrationsHonorSearchPath pins the connection search_path to a
// dedicated schema and verifies that every DDL statement lands there, never
// in public. This is the AD-9 premise: test factories must be able to run
// each suite in its own schema.
func TestE2EMigrationsHonorSearchPath(t *testing.T) {
	pool := newScratchPool(t)

	schema := "e2e_iso"
	if _, err := pool.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolation schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})

	// Reconnect with the search_path pinned to the isolation schema.
	cfg, err := pgxpool.ParseConfig(poolURL(t))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("connect with pinned search_path: %v", err)
	}
	defer isolated.Close()

	if err := migrations.Up(t.Context(), isolated); err != nil {
		t.Fatalf("Up in isolated schema: %v", err)
	}

	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if !relationExistsIn(t, pool, schema, table) {
			t.Errorf("expected %s in schema %s", table, schema)
		}
		if relationExistsIn(t, pool, "public", table) {
			t.Errorf("table %s leaked into public schema", table)
		}
	}
	for _, index := range expectedIndexes {
		if !indexExistsIn(t, pool, schema, index) {
			t.Errorf("expected index %s in schema %s", index, schema)
		}
		if indexExistsIn(t, pool, "public", index) {
			t.Errorf("index %s leaked into public schema", index)
		}
	}
	assertAppliedVersions(t, isolated, []string{"000001", "000002"})

	// Down removes the objects from the isolated schema only.
	if err := migrations.Down(t.Context(), isolated); err != nil {
		t.Fatalf("Down in isolated schema: %v", err)
	}
	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if relationExistsIn(t, pool, schema, table) {
			t.Errorf("expected %s to be gone from schema %s", table, schema)
		}
	}
}

// TestE2ESchemaContract verifies the column shapes the store adapters will
// depend on: handle-based primary keys, jsonb payload columns, nullable
// expiry columns, the version guard columns, and the sessions text array.
func TestE2ESchemaContract(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// table -> column -> expected udt_name (information_schema.collations
	// convention; text[] reports as _text).
	contract := map[string]map[string]string{
		"authorization_codes": {
			"handle":     "text",
			"expires_at": "timestamptz",
			"payload":    "jsonb",
		},
		"refresh_tokens": {
			"handle":     "text",
			"expires_at": "timestamptz",
			"version":    "int8",
			"payload":    "jsonb",
		},
		"reference_tokens": {
			"handle":     "text",
			"expires_at": "timestamptz",
			"payload":    "jsonb",
		},
		"user_consents": {
			"subject_id": "text",
			"client_id":  "text",
			"expires_at": "timestamptz",
			"payload":    "jsonb",
		},
		"persisted_grants": {
			"key":        "text",
			"subject_id": "text",
			"session_id": "text",
			"client_id":  "text",
			"type":       "text",
			"expires_at": "timestamptz",
			"payload":    "jsonb",
		},
		"device_codes": {
			"handle":     "text",
			"user_code":  "text",
			"expires_at": "timestamptz",
			"payload":    "jsonb",
		},
		"backchannel_auth_requests": {
			"handle":     "text",
			"expires_at": "timestamptz",
			"payload":    "jsonb",
		},
		"sessions": {
			"session_id": "text",
			"client_ids": "_text",
			"expires":    "timestamptz",
			"payload":    "jsonb",
		},
		"key_records": {
			"handle":    "text",
			"public_id": "text",
			"phase":     "text",
			"version":   "int8",
			"payload":   "jsonb",
		},
	}

	for table, columns := range contract {
		for column, wantType := range columns {
			got := columnType(t, pool, table, column)
			if got == "" {
				t.Errorf("table %s is missing column %s", table, column)
				continue
			}
			if got != wantType {
				t.Errorf("table %s column %s has type %s, want %s", table, column, got, wantType)
			}
		}
	}

	// user_consents uses the natural-key composite primary key.
	if !hasPrimaryKey(t, pool, "user_consents", "subject_id", "client_id") {
		t.Errorf("user_consents lacks composite PK (subject_id, client_id)")
	}
}

// TestE2EKeyRecordsPartialUniqueIndex verifies the partial unique index on
// public_id: non-retired records must not share a public_id, while a retired
// record may coexist with a live one.
func TestE2EKeyRecordsPartialUniqueIndex(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	const insert = `INSERT INTO key_records (handle, public_id, phase, version, payload)
		VALUES ($1, $2, $3, 1, '{"version":1}'::jsonb)`

	if _, err := pool.Exec(t.Context(), insert, "h-1", "pub-1", "active"); err != nil {
		t.Fatalf("insert first record: %v", err)
	}
	// A second non-retired record with the same public_id must conflict.
	if _, err := pool.Exec(t.Context(), insert, "h-2", "pub-1", "active"); err == nil {
		t.Errorf("expected unique violation for duplicate non-retired public_id")
	}
	// A retired record may reuse the public_id.
	if _, err := pool.Exec(t.Context(), insert, "h-3", "pub-1", "retired"); err != nil {
		t.Errorf("retired record with reused public_id should be allowed: %v", err)
	}
	// Two retired records with the same public_id are also allowed.
	if _, err := pool.Exec(t.Context(), insert, "h-4", "pub-1", "retired"); err != nil {
		t.Errorf("second retired record with reused public_id should be allowed: %v", err)
	}
}

// TestE2EUpRespectsCanceledContext verifies the migration entry point honors
// its context: a canceled context fails fast instead of silently succeeding.
func TestE2EUpRespectsCanceledContext(t *testing.T) {
	pool := newScratchPool(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := migrations.Up(ctx, pool); err == nil {
		t.Errorf("Up with canceled context should fail, got nil")
	}
	assertObjectsAbsent(t, pool)
}

// scratchURL holds the URL of the throwaway database created by the most
// recent newScratchPool call, for building derived connections (e.g.
// search_path pins) that must target the same database.
var scratchURL string

// newScratchPool creates a throwaway database from the admin URL, connects
// to it, and registers cleanup that drops it.
func newScratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	adminURL := os.Getenv(testDatabaseURLEnv)
	if adminURL == "" {
		t.Skipf("%s not set; skipping real-database e2e tests", testDatabaseURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to admin database: %v", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("wicket_pg_e2e_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP DATABASE "+dbName)
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	u.Path = "/" + dbName
	scratchURL = u.String()
	pool, err := pgxpool.New(ctx, scratchURL)
	if err != nil {
		t.Fatalf("connect to scratch database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// poolURL returns the URL of the scratch database most recently created by
// newScratchPool, for building derived connections (e.g. search_path pins).
func poolURL(t *testing.T) string {
	t.Helper()
	if scratchURL == "" {
		t.Fatal("poolURL called before newScratchPool")
	}
	return scratchURL
}

func assertObjectsPresent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if !relationExists(t, pool, table) {
			t.Errorf("expected table %s to exist after Up", table)
		}
	}
	for _, index := range expectedIndexes {
		if !indexExists(t, pool, index) {
			t.Errorf("expected index %s to exist after Up", index)
		}
	}
}

func assertObjectsAbsent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if relationExists(t, pool, table) {
			t.Errorf("expected table %s to be gone after Down", table)
		}
	}
	for _, index := range expectedIndexes {
		if indexExists(t, pool, index) {
			t.Errorf("expected index %s to be gone after Down", index)
		}
	}
}

func assertAppliedVersions(t *testing.T, pool *pgxpool.Pool, want []string) {
	t.Helper()
	rows, err := pool.Query(t.Context(), "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan applied version: %v", err)
		}
		got = append(got, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read applied versions: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("applied versions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applied versions = %v, want %v", got, want)
		}
	}
}

func relationExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	return relationExistsIn(t, pool, "public", name)
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

func indexExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	return indexExistsIn(t, pool, "public", name)
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

func columnType(t *testing.T, pool *pgxpool.Pool, table, column string) string {
	t.Helper()
	var udt string
	err := pool.QueryRow(t.Context(),
		"SELECT udt_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2",
		table, column).Scan(&udt)
	if err != nil {
		return ""
	}
	return udt
}

func hasPrimaryKey(t *testing.T, pool *pgxpool.Pool, table string, columns ...string) bool {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT a.attname
		 FROM pg_index i
		 JOIN pg_class c ON c.oid = i.indrelid
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(i.indkey)
		 WHERE n.nspname = 'public' AND c.relname = $1 AND i.indisprimary
		 ORDER BY array_position(i.indkey, a.attnum)`,
		table)
	if err != nil {
		t.Fatalf("query primary key of %s: %v", table, err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan primary key column: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read primary key columns: %v", err)
	}
	if len(got) != len(columns) {
		return false
	}
	for i := range columns {
		if got[i] != columns[i] {
			return false
		}
	}
	return true
}
