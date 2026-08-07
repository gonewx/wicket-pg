// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package migrations audits the schema DDL and the store cleanup statements
// for the architecture rule that SQL never consults the database clock:
// expiry derivation and reclamation are driven by caller-supplied values,
// and every cleanup statement has the single unified shape
// "DELETE ... WHERE expires_at IS NOT NULL AND expires_at < $1" (sessions:
// the expires column). Both tests are pure static scans of the embedded SQL
// and of ../store sources — they never touch a database, so they must run
// in every environment.
package migrations

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// clockTokens are the SQL clock functions banned by AD-5. The fragments are
// assembled at runtime so this file's own source cannot be hit by the audit
// it performs (the lineage gate's self-hit lesson).
var clockTokens = []string{
	"n" + "ow(",
	"CURRENT" + "_TIMESTAMP",
	"clock" + "_timestamp",
	"statement" + "_timestamp",
	"transaction" + "_timestamp",
	"time" + "ofday(",
	"current" + "_date",
	"current" + "_time",
	"local" + "time",
	"local" + "timestamp",
}

// TestClockAuditMigrationsSQL asserts that no embedded migration file
// contains any clock function token. Expiry is always derived by the caller
// and passed as a parameter; the DDL must never fall back to the database
// clock.
func TestClockAuditMigrationsSQL(t *testing.T) {
	names, err := fs.Glob(sqlFiles, "*.sql")
	if err != nil {
		t.Fatalf("list embedded migration files: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no embedded migration files found")
	}
	for _, name := range names {
		body, err := sqlFiles.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		checkNoClockTokens(t, name, string(body))
	}
}

// TestClockAuditStoreCleanupSQL asserts that every store source file with a
// cleanup method contains exactly the unified DELETE shape — an IS NOT NULL
// guard, strict less-than against the parameterized cutoff, and no clock
// function anywhere in the file. The session store uses its expires column;
// all other stores use expires_at.
func TestClockAuditStoreCleanupSQL(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "store", "*.go"))
	if err != nil {
		t.Fatalf("list store sources: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no store source files found")
	}
	cleanupFiles := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		src := string(body)
		if !strings.Contains(src, "RemoveExpired") && !strings.Contains(src, "DeleteExpired") {
			continue
		}
		cleanupFiles++
		checkNoClockTokens(t, file, src)

		// The cleanup statement is the only DELETE in these files with the
		// IS NOT NULL guard; assert its exact unified shape. Grant-family
		// stores use the expires_at column, the session store its expires
		// column — the two forms are pinned separately so a mixed-column
		// statement cannot slip through.
		grantShape := regexp.MustCompile(`"DELETE FROM \w+ WHERE expires_at IS NOT NULL AND expires_at < \$1"`)
		sessionShape := regexp.MustCompile(`"DELETE FROM \w+ WHERE expires IS NOT NULL AND expires < \$1"`)
		if !grantShape.MatchString(src) && !sessionShape.MatchString(src) {
			t.Errorf("%s: cleanup statement is not the unified shape "+
				`"DELETE FROM <table> WHERE expires_at IS NOT NULL AND expires_at < $1"`, file)
		}
		// No variant may appear: no <=, no bare WHERE without the guard,
		// no hardcoded timestamp, no INTERVAL arithmetic. Clock tokens
		// (now(, CURRENT_*, ...) are already covered by checkNoClockTokens
		// above; a bare CURRENT scan would false-positive on comments
		// mentioning "concurrent".
		for _, variant := range []string{
			"expires_at <=", "expires <=", "expires_at IS NULL", "expires IS NULL",
			"INTERVAL",
		} {
			if strings.Contains(src, variant) {
				t.Errorf("%s: cleanup statement contains forbidden variant %q", file, variant)
			}
		}
	}
	if cleanupFiles != 8 {
		t.Errorf("expected 8 store files with cleanup methods, audited %d", cleanupFiles)
	}
}

// checkNoClockTokens reports any clock function token found in the given
// content, with a case-insensitive scan (SQL keywords may be written in any
// case).
func checkNoClockTokens(t *testing.T, what, content string) {
	t.Helper()
	upper := strings.ToUpper(content)
	for _, token := range clockTokens {
		if strings.Contains(upper, strings.ToUpper(token)) {
			t.Errorf("%s: contains clock function token %q", what, token)
		}
	}
}
