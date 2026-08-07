// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package migrations embeds the schema DDL and applies it on a single
// connection so that every statement lands in that connection's current
// search_path. Hosts call Up and Down explicitly; the package never starts
// background work on its own.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var sqlFiles embed.FS

// Up applies every pending migration file in version order. Each file runs
// in its own transaction on a single pooled connection; versions already
// recorded in schema_migrations are skipped, so repeated calls are
// idempotent.
func Up(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrations: acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL
	)`); err != nil {
		return fmt.Errorf("migrations: create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn.Conn())
	if err != nil {
		return err
	}

	files, err := migrationFiles(".up.sql")
	if err != nil {
		return err
	}
	for _, name := range files {
		version := versionOf(name)
		if applied[version] {
			continue
		}
		body, err := sqlFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("migrations: read %s: %w", name, err)
		}
		if err := applyFile(ctx, conn.Conn(), name, string(body), version); err != nil {
			return err
		}
	}
	return nil
}

// Down rolls back applied migrations in reverse version order, mirroring
// each up file with its down counterpart and removing the bookkeeping rows
// in the same transaction. A database that has never been migrated is a
// no-op.
func Down(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrations: acquire connection: %w", err)
	}
	defer conn.Release()

	var reg *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('schema_migrations')`).Scan(&reg); err != nil {
		return fmt.Errorf("migrations: check schema_migrations: %w", err)
	}
	if reg == nil {
		return nil
	}

	applied, err := appliedVersions(ctx, conn.Conn())
	if err != nil {
		return err
	}

	files, err := migrationFiles(".down.sql")
	if err != nil {
		return err
	}
	for i := len(files) - 1; i >= 0; i-- {
		name := files[i]
		version := versionOf(name)
		if !applied[version] {
			continue
		}
		body, err := sqlFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("migrations: read %s: %w", name, err)
		}
		if err := revertFile(ctx, conn.Conn(), name, string(body), version); err != nil {
			return err
		}
	}
	return nil
}

func applyFile(ctx context.Context, conn *pgx.Conn, name, body, version string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrations: begin %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, body); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migrations: apply %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
		version, time.Now().UTC()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migrations: record %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrations: commit %s: %w", name, err)
	}
	return nil
}

func revertFile(ctx context.Context, conn *pgx.Conn, name, body, version string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrations: begin %s: %w", name, err)
	}
	// Unrecord the version before running the down file: the file itself
	// drops schema_migrations last, mirroring the up file in reverse.
	if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migrations: unrecord %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, body); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migrations: revert %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrations: commit %s: %w", name, err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrations: query applied versions: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("migrations: scan applied version: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrations: read applied versions: %w", err)
	}
	return applied, nil
}

func migrationFiles(suffix string) ([]string, error) {
	names, err := fs.Glob(sqlFiles, "*"+suffix)
	if err != nil {
		return nil, fmt.Errorf("migrations: list %s files: %w", suffix, err)
	}
	sort.Strings(names)
	return names, nil
}

// versionOf extracts the version prefix from a migration file name, e.g.
// "000001_init.up.sql" -> "000001".
func versionOf(name string) string {
	if i := strings.IndexByte(name, '_'); i >= 0 {
		return name[:i]
	}
	return name
}
