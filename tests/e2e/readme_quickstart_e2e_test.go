// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test — story 3.1 README quick start, verified end to end.
//
// The README is the first thing a host developer reads: its Quick Start
// claims an install command, a migration entry point, and eleven store
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

// wantReleaseVersion is the release this README documents: v0.3.0, which
// adds the pushed authorization request and client registration stores
// (migrations 000005 and 000006) and still requires wicket v0.8.0 (suite
// version 3.0.0). v0.2.0 was the first release for wicket v0.8.0 and v0.2.1
// corrected its upgrade notes on refresh tokens. Hosts still on wicket
// v0.1.x keep v0.1.3, which the upgrade section names separately.
const wantReleaseVersion = "v0.3.0"

// TestE2EReadmeInstallCommandPinsRealModule verifies AC-1: the install
// command uses the GOWORK=off form (the external workspace would otherwise
// shadow the module) and names the module path and version that go.mod
// actually declares. v0.1.0's first tag pointed at a pre-revert tree that the
// module proxy cached, so consumers fetching it got a broken go 1.27 directive
// (see story 3-2 Review Findings) — which is why the documented version is
// asserted here rather than left to drift.
func TestE2EReadmeInstallCommandPinsRealModule(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))
	goMod := readFile(t, filepath.Join(repoRoot(t), "go.mod"))

	// Built from the module path go.mod actually declares, and anchored on a
	// word boundary: a bare substring check would also accept a prerelease
	// such as @v0.2.0-rc.1, letting it satisfy the check.
	install := "GOWORK=off go get " + modulePathOf(t, goMod) + "@" + wantReleaseVersion
	if !regexp.MustCompile(regexp.QuoteMeta(install) + `(\s|$)`).MatchString(readme) {
		t.Errorf("README must show the install command %q", install)
	}

	// The Status section names the version too. Without it, only the install
	// command would move on a release and the document would advertise one
	// version while installing another. Anchored like the install command, so
	// "use v0.2.0-rc.1" does not pass; a sentence-ending period is allowed.
	claim := "use " + wantReleaseVersion
	if !regexp.MustCompile(regexp.QuoteMeta(claim) + `([\s,;:)]|\.(\s|$)|$)`).MatchString(readme) {
		t.Errorf("README must carry the version claim %q", claim)
	}

	// Prerequisites names the wicket version this module actually requires,
	// read from go.mod, so a dependency bump fails here until the README moves.
	if claim := "this module requires wicket " + requiredVersionOf(t, goMod, "github.com/gonewx/wicket"); !strings.Contains(readme, claim) {
		t.Errorf("README Prerequisites must carry %q", claim)
	}

	// The go directive history is a fixed fact about past tags, not about the
	// documented release: v0.1.3 was the first tag whose directive names the
	// Go 1.27.0 release, so the claim stays pinned to v0.1.3 on every release.
	if claim := "from v0.1.3 onward"; !strings.Contains(readme, claim) {
		t.Errorf("README must carry the go directive history claim %q", claim)
	}
}

// TestE2EReadmeUpgradeSectionNamesMigrationAndFallback verifies the upgrade
// guidance a v0.1.x host depends on: stop every old writer first (rows an old
// build writes after the migration are never backfilled), run the NUL
// pre-check (a payload holding \u0000 rolls the whole migration back), apply migration
// 000004 (the removal-by-client and removal-by-session methods query the
// locator columns it adds and backfills), never back out with
// migrations.Down (it drops every table), stay on v0.1.3 while wicket v0.1.x
// is in use (v0.2.x raises the host's wicket requirement to v0.8.0), and
// backfill refresh tokens issued by wicket v0.1.x before calling
// RemoveExpired on them (their derived expiry is in year 1, so the first
// RemoveExpired would delete every one of them).
func TestE2EReadmeUpgradeSectionNamesMigrationAndFallback(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	for _, claim := range []string{
		"Stop every writer still running a v0.1.x build",
		"Run the NUL pre-check",
		"apply migration 000004",
		"Do not use `migrations.Down` to back out `000004`",
		"stay on v0.1.3",
		"Do not call `RemoveExpired` on refresh tokens before the backfill",
	} {
		if !strings.Contains(readme, claim) {
			t.Errorf("README upgrade guidance must carry %q", claim)
		}
	}
}

// TestE2EReadmeCarriesClientSeedingGuidance pins the guidance a host needs
// before enabling dynamic client registration: the registration store then
// becomes the only client source (wicket panics at Freeze when it is
// combined with SetClientStore, SetClients or SetClientsProvider), so every
// existing client must be seeded with RegisterClient first. The README must
// say how: secrets hashed by the caller, the empty digest that makes the
// management endpoints refuse the client or a digest computed by wicket's
// helper, a non-zero registration time, ErrDuplicateHandle on a rerun, and
// tombstoned ids that cannot be seeded again.
func TestE2EReadmeCarriesClientSeedingGuidance(t *testing.T) {
	readme := readFile(t, filepath.Join(repoRoot(t), readmePath))

	for _, claim := range []string{
		"Seed existing clients before enabling dynamic client registration",
		"the client registration store is the only client source",
		"one `RegisterClient` call per client",
		"must already hold the hashed secret",
		"leave it empty (`\"\"`)",
		"`clientregistration.RegistrationAccessTokenDigest(token)`",
		"not the zero value",
		"`client_id_issued_at`",
		"returns `storage.ErrDuplicateHandle` for each client already present",
		"`FindClientRegistration`",
		"`UpdateClientRegistration`",
		"cannot be seeded again",
	} {
		if !strings.Contains(readme, claim) {
			t.Errorf("README client seeding guidance must carry %q", claim)
		}
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
	if len(constructors) != 11 {
		t.Fatalf("store package exports %d constructors, want 11: %v", len(constructors), constructors)
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

// requiredVersionOf returns the version go.mod requires for module, from
// either a single-line require or a require block entry.
func requiredVersionOf(t *testing.T, goMod, module string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*(?:require\s+)?` + regexp.QuoteMeta(module) + `\s+(v\S+)`)
	m := re.FindStringSubmatch(goMod)
	if m == nil {
		t.Fatalf("go.mod does not require %s", module)
	}
	return m[1]
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
