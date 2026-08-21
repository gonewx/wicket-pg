// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package conformance provides the isolated-schema store factory that the
// wicket port conformance suites run against. Every factory call creates a
// brand-new schema, a dedicated pool pinned to it, and a fresh empty store,
// so parallel suite cases never observe each other's state.
//
// The suites invoke the returned factory from parallel subtests, so the
// factory must be safe from foreign goroutines: it registers teardown via
// t.Cleanup (mutex-protected) and fails by panicking (recovered by the
// testing framework) instead of calling t.Fatal.
package conformance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabaseURLEnv names the admin database the factory builds isolated
// schemas in; without it the factory skips instead of failing, matching the
// e2e suite convention.
const testDatabaseURLEnv = "WICKET_PG_TEST_DATABASE_URL"

// NewStore returns a store factory for a conformance suite. Every call of
// the returned function creates a fresh isolated environment: a unique
// schema on the database named by WICKET_PG_TEST_DATABASE_URL, a dedicated
// pool pinned to that schema via connection-level search_path, the
// migrations applied inside it, and construct invoked with the ready pool.
// The environment is torn down when the test completes.
//
// construct must follow the store constructor shape
//
//	func(pool *pgxpool.Pool, logger *slog.Logger) T
//
// so the nine store constructors can be passed directly.
func NewStore[T any](t *testing.T, construct func(pool *pgxpool.Pool, logger *slog.Logger) T) func() T {
	t.Helper()

	baseURL := os.Getenv(testDatabaseURLEnv)
	if baseURL == "" {
		t.Skipf("%s not set; skipping conformance tests", testDatabaseURLEnv)
	}

	// The management pool stays on the default search_path: it creates and
	// drops the unique schemas by name, so it must not be pinned anywhere.
	admin, err := pgxpool.New(t.Context(), baseURL)
	if err != nil {
		t.Fatalf("conformance: connect management pool: %v", err)
	}
	t.Cleanup(admin.Close)

	// Discard logging keeps suite output clean; store adapters must not
	// emit credentials at any level.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return func() T {
		name := uniqueSchemaName()

		// The schema cannot be created through a connection pinned to it;
		// create it via the management pool first.
		if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
			panic(fmt.Errorf("conformance: create schema %s: %w", name, err))
		}

		cfg, err := pgxpool.ParseConfig(baseURL)
		if err != nil {
			panic(fmt.Errorf("conformance: parse base config: %w", err))
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = name
		pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			panic(fmt.Errorf("conformance: connect with search_path %s: %w", name, err))
		}

		// Teardown runs once the whole test (including parallel subtests)
		// finishes; t.Context() is already canceled there, so the drop uses
		// a fresh context. DROP SCHEMA resolves by name, so the management
		// pool can execute it regardless of its own search_path. Cleanups run
		// LIFO, so the management pool registered above is still open here.
		//
		// A failed drop is reported rather than panicked: panicking mid-cleanup
		// would skip the remaining schemas' teardown and leak more than it
		// reports. Silence is not an option either — a drop that keeps failing
		// accumulates dead schemas in the test database run after run.
		t.Cleanup(func() {
			pool.Close()
			if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
				t.Errorf("conformance: drop schema %s: %v", name, err)
			}
		})

		if err := migrations.Up(t.Context(), pool); err != nil {
			panic(fmt.Errorf("conformance: migrate schema %s: %w", name, err))
		}

		return construct(pool, logger)
	}
}

// uniqueSchemaName builds a lowercase identifier unique across concurrent
// calls: a timestamp plus 16 random hex digits. The restricted charset keeps
// the name unquoted and parallel-safe.
func uniqueSchemaName() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("conformance: read random schema suffix: %w", err))
	}
	return fmt.Sprintf("conformance_%d_%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}
