// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package lineage_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileHeadersAssertOriginalAuthorship verifies that all Go source files
// contain the required copyright header asserting original authorship.
func TestFileHeadersAssertOriginalAuthorship(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	expectedHeader := "// Copyright 2026 Decker."

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip non-Go files, vendor, and hidden directories
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		lines := strings.Split(string(content), "\n")
		if len(lines) == 0 {
			t.Errorf("%s: file is empty", path)
			return nil
		}

		if !strings.Contains(lines[0], expectedHeader) {
			t.Errorf("%s: first line must contain %q, got: %s", path, expectedHeader, lines[0])
		}

		return nil
	})

	if err != nil {
		t.Fatalf("Failed to walk repository: %v", err)
	}
}

// TestNoUpstreamLineageMarkers ensures that no Go source file contains
// markers indicating derivation from upstream sources.
func TestNoUpstreamLineageMarkers(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)

	// Split literals so that this table does not match itself when the gate
	// walks its own source file.
	forbiddenPhrases := []string{
		"Based " + "on",
		"Ported " + "from",
		"Derived " + "from",
		"Adapted " + "from",
		"Due" + "nde",
		"Identity" + "Server",
	}

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for lineNo, line := range strings.Split(string(content), "\n") {
			for _, phrase := range forbiddenPhrases {
				if strings.Contains(line, phrase) {
					t.Errorf("%s:%d: contains forbidden upstream lineage marker %q: %s",
						path, lineNo+1, phrase, line)
				}
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("Failed to walk repository: %v", err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Walk up from tests/lineage to find repo root (contains go.mod)
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

// TestCommitMessagesStayNeutral scans the repository's own commit history for
// upstream vendor names and for process vocabulary that belongs in the private
// evidence ledger rather than in a public, permanently-published git history.
//
// A shallow CI clone sees only part of the history; that is acceptable — the
// gate is a safety net over what is present, not a proof about all of it.
func TestCommitMessagesStayNeutral(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)

	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Skip("not a git checkout; nothing to scan")
	}

	cmd := exec.Command("git", "log", "--format=%H%x00%B%x1e", "--no-merges")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git log unavailable: %v", err)
	}

	// Split assembled so the terms below never appear literally in this file.
	forbidden := []struct {
		name  string
		value string
	}{
		{name: "vendor name", value: "Due" + "nde"},
		{name: "upstream product", value: "Identity" + "Server"},
		{name: "lineage class marker", value: "桶 " + "A"},
		{name: "lineage class marker", value: "桶 " + "B"},
		{name: "lineage class marker", value: "桶 " + "C"},
		{name: "isolation term", value: "净" + "室"},
		{name: "isolation term", value: "clean" + "-room"},
	}

	for _, entry := range strings.Split(string(out), "\x1e") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		hash, body, found := strings.Cut(entry, "\x00")
		if !found {
			continue
		}
		for _, f := range forbidden {
			if strings.Contains(body, f.value) {
				t.Errorf("commit %s message contains %s %q", hash[:min(8, len(hash))], f.name, f.value)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
