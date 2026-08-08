// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test — story 3.1 README quick start, verified end to end.
//
// The README is the first thing a host developer reads: its Quick Start
// claims an install command, a migration entry point, and nine store
// constructors that must match the real module exactly — a wrong signature
// here is a broken integration for every first-time adopter. What unit
// tests cannot prove is that the document stays truthful as the code
// evolves. These tests pin the README contract end to end: every
// constructor the store package actually exports appears in the README
// with the documented call shape, the migration entry points exist with
// the documented signatures, the install command names the real module,
// and the version claims (PostgreSQL 15+, no stale planning language, no
// lineage markers) hold. All assertions are static reads of the README
// and the Go sources, so the suite runs anywhere with no database.
package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const readmePath = "README.md"

// TestE2EReadmeInstallCommandPinsRealModule verifies AC-1: the install
// command uses the GOWORK=off form (the external workspace would otherwise
// shadow the module) and names the module path and version that go.mod
// actually declares. The pinned version is v0.1.2: v0.1.0's first tag
// pointed at a pre-revert tree that the module proxy cached, so consumers
// fetching it got a broken go 1.27 directive; v0.1.2 is the consumable
// release (see story 3-2 Review Findings).
func TestE2EReadmeInstallCommandPinsRealModule(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	if !strings.Contains(readme, "GOWORK=off go get github.com/gonewx/wicket-pg@v0.1.2") {
		t.Error("README must show the install command 'GOWORK=off go get github.com/gonewx/wicket-pg@v0.1.2'")
	}

	goMod := readFile(t, filepath.Join(repoRoot(t), "go.mod"))
	modulePath := modulePathOf(t, goMod)
	if !strings.Contains(readme, "go get "+modulePath) {
		t.Errorf("README install command does not name the real module path %s", modulePath)
	}
}

// TestE2EReadmeMigrationExampleMatchesSource verifies AC-2: the README
// shows both migrations.Up and migrations.Down called on a host-owned
// pgxpool.Pool, and the source exports those exact signatures — a host
// following the document compiles against the real entry points.
func TestE2EReadmeMigrationExampleMatchesSource(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	for _, call := range []string{"migrations.Up(ctx, pool)", "migrations.Down(ctx, pool)"} {
		if !strings.Contains(readme, call) {
			t.Errorf("README must show the call %q", call)
		}
	}

	migrationsSource := readFile(t, filepath.Join(repoRoot(t), "migrations/migrations.go"))
	for _, want := range []string{
		"func Up(ctx context.Context, pool *pgxpool.Pool) error",
		"func Down(ctx context.Context, pool *pgxpool.Pool) error",
	} {
		if !strings.Contains(migrationsSource, want) {
			t.Errorf("migrations source must export %q; README documents it", want)
		}
	}
}

// TestE2EReadmeListsEveryStoreConstructor verifies AC-3: every store
// constructor the store package exports appears in the README's injection
// example with the documented call shape store.NewXxxStore(pool, logger).
// The constructor list is derived from the source, not hardcoded, so a
// new store family added later fails this test until the README catches up.
func TestE2EReadmeListsEveryStoreConstructor(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	constructors := storeConstructors(t)
	if len(constructors) != 9 {
		t.Fatalf("store package exports %d constructors, want 9: %v", len(constructors), constructors)
	}
	for _, ctor := range constructors {
		call := "store." + ctor + "(pool, logger)"
		if !strings.Contains(readme, call) {
			t.Errorf("README injection example missing %q", call)
		}
	}
}

// TestE2EReadmeStatesPostgres15AndNoStaleVersion verifies AC-4: the README
// states PostgreSQL 15+ as the minimum and contains no stale "12+"
// expression anywhere in the document.
func TestE2EReadmeStatesPostgres15AndNoStaleVersion(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	if !strings.Contains(readme, "PostgreSQL 15+") {
		t.Error("README must state 'PostgreSQL 15+' as the minimum version")
	}
	if strings.Contains(readme, "12+") {
		t.Error("README must not contain the stale '12+' version expression")
	}
}

// TestE2EReadmeHasNoStalePlanningLanguage verifies AC-5: the Status and
// Quick Start sections reflect the completed implementation — no
// development-in-progress placeholder, no promise of future completion
// when the adapter lands, no reference to a not-yet-built epic.
func TestE2EReadmeHasNoStalePlanningLanguage(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	for _, stale := range []string{
		"will be completed once the adapter implementation lands",
		"Development in progress",
		"next piece of work",
		"Epic 9",
	} {
		if strings.Contains(readme, stale) {
			t.Errorf("README must not contain stale planning language %q", stale)
		}
	}
}

// TestE2EReadmeHasNoLineageMarkers verifies AC-6/NFR-6: the README contains
// no phrase asserting derivation from an upstream implementation. The
// forbidden list mirrors the lineage gate's table, assembled by
// concatenation so this test cannot trip over its own assertions.
func TestE2EReadmeHasNoLineageMarkers(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	forbidden := []string{
		"Based " + "on",
		"Ported " + "from",
		"Derived " + "from",
		"Adapted " + "from",
		"Due" + "nde",
		"Identity" + "Server",
	}
	for _, phrase := range forbidden {
		if strings.Contains(readme, phrase) {
			t.Errorf("README contains forbidden lineage marker %q", phrase)
		}
	}
}

// storeConstructors returns the names of every New<X>Store constructor
// exported by the store package, sorted for stable output.
func storeConstructors(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "store", "*.go"))
	if err != nil {
		t.Fatalf("glob store sources: %v", err)
	}

	ctorRe := regexp.MustCompile(`func (New\w+Store)\(pool \*pgxpool\.Pool, logger \*slog\.Logger\)`)
	var names []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range ctorRe.FindAllSubmatch(src, -1) {
			names = append(names, string(m[1]))
		}
	}
	sort.Strings(names)
	return names
}

// modulePathOf extracts the module path from the go.mod module directive.
func modulePathOf(t *testing.T, goMod string) string {
	t.Helper()
	for _, line := range strings.Split(goMod, "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	t.Fatal("go.mod has no module directive")
	return ""
}
