// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package migrations_test

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

// The suite exercises Up/Down against a real PostgreSQL server. It is gated
// behind WICKET_PG_TEST_DATABASE_URL so that the package test run stays
// green without a database: point the variable at an admin database (the
// role needs CREATE DATABASE) and a throwaway database is created, migrated,
// rolled back, and dropped.
const testDatabaseURLEnv = "WICKET_PG_TEST_DATABASE_URL"

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

func TestMigrationsUpIdempotentDownReup(t *testing.T) {
	adminURL := os.Getenv(testDatabaseURLEnv)
	if adminURL == "" {
		t.Skipf("%s not set; skipping real-database migration tests", testDatabaseURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to admin database: %v", err)
	}
	defer admin.Close()

	dbName := fmt.Sprintf("wicket_pg_migtest_%d", time.Now().UnixNano())
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
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("connect to scratch database: %v", err)
	}
	defer pool.Close()

	// AC-1: a single Up on an empty database creates every object and
	// records the applied version.
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	assertObjectsPresent(t, ctx, pool)
	assertAppliedVersions(t, ctx, pool, []string{"000001", "000002", "000003"})

	// AC-1: a second Up is a no-op and must not error or duplicate objects.
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	assertObjectsPresent(t, ctx, pool)
	assertAppliedVersions(t, ctx, pool, []string{"000001", "000002", "000003"})

	// AC-3: Down removes everything and clears the bookkeeping table.
	if err := migrations.Down(ctx, pool); err != nil {
		t.Fatalf("Down: %v", err)
	}
	assertObjectsAbsent(t, ctx, pool)

	// The database can be rebuilt from scratch afterwards.
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}
	assertObjectsPresent(t, ctx, pool)
	assertAppliedVersions(t, ctx, pool, []string{"000001", "000002", "000003"})

	// Down on an already-clean database is a harmless no-op.
	if err := migrations.Down(ctx, pool); err != nil {
		t.Fatalf("Down on clean database: %v", err)
	}
}

func assertObjectsPresent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if !relationExists(t, ctx, pool, table) {
			t.Errorf("expected table %s to exist after Up", table)
		}
	}
	for _, index := range expectedIndexes {
		if !indexExists(t, ctx, pool, index) {
			t.Errorf("expected index %s to exist after Up", index)
		}
	}
}

func assertObjectsAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range append([]string{"schema_migrations"}, businessTables...) {
		if relationExists(t, ctx, pool, table) {
			t.Errorf("expected table %s to be gone after Down", table)
		}
	}
	for _, index := range expectedIndexes {
		if indexExists(t, ctx, pool, index) {
			t.Errorf("expected index %s to be gone after Down", index)
		}
	}
}

func assertAppliedVersions(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want []string) {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
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

func relationExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)",
		name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}

func indexExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1)",
		name).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %s: %v", name, err)
	}
	return exists
}
