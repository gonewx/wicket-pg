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

// The probe tests below drive the real gates as subprocesses against
// temporary violation fixtures and assert red-then-green. They must NOT
// call t.Parallel(): the walk gates in lineage_test.go run in parallel,
// and a probe's temporary violation files must not overlap their scan
// window or the whole run would turn red for the wrong reason. Go runs
// every non-parallel test to completion before any parallel test starts,
// so keeping the probes non-parallel is sufficient on its own — file
// position is irrelevant (tests run in file-name order, so these probes
// actually execute before the gates). Note that concurrent `go test`
// invocations over the same checkout (e.g. CI and a developer shell)
// can still observe each other's fixtures; do not run lineage tests
// concurrently on one working tree.

// TestFileHeaderGateDetectsViolation proves that the file-header gate
// turns red while a headerless file exists in the tree and green again
// once it is removed.
func TestFileHeaderGateDetectsViolation(t *testing.T) {
	repoRoot := findRepoRoot(t)
	probeDir := filepath.Join(repoRoot, "probe-tmp")
	// Scrub stale leftovers from a hard-killed previous run so a residual
	// violation file can never redden the gates on its own.
	if err := os.RemoveAll(probeDir); err != nil {
		t.Fatalf("RemoveAll %s: %v", probeDir, err)
	}
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", probeDir, err)
	}
	defer os.RemoveAll(probeDir)

	badFile := filepath.Join(probeDir, "bad_header.go")
	if err := os.WriteFile(badFile, []byte("package probe\n"), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", badFile, err)
	}

	// Red: a headerless file must fail the file-header gate.
	out, err := runGate(t, repoRoot, "^TestFileHeadersAssertOriginalAuthorship$")
	assertGateRed(t, out, err, "file-header gate", "TestFileHeadersAssertOriginalAuthorship")

	// Green: after removal the same command passes.
	if err := os.Remove(badFile); err != nil {
		t.Fatalf("Remove %s: %v", badFile, err)
	}
	if out, err := runGate(t, repoRoot, "^TestFileHeadersAssertOriginalAuthorship$"); err != nil {
		t.Errorf("file-header gate stayed red after the violation was removed: %v\n%s", err, out)
	}
}

// TestMarkerGateDetectsViolation proves that the marker gate turns red
// while a file carrying an upstream-derivation marker exists and green
// again once it is removed.
func TestMarkerGateDetectsViolation(t *testing.T) {
	repoRoot := findRepoRoot(t)
	probeDir := filepath.Join(repoRoot, "probe-tmp")
	// Scrub stale leftovers from a hard-killed previous run so a residual
	// violation file can never redden the gates on its own.
	if err := os.RemoveAll(probeDir); err != nil {
		t.Fatalf("RemoveAll %s: %v", probeDir, err)
	}
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", probeDir, err)
	}
	defer os.RemoveAll(probeDir)

	// The violating marker is assembled at runtime so this file never
	// contains the literal phrase the gate searches for.
	marker := "// " + "Based " + "on: x"
	badFile := filepath.Join(probeDir, "bad_marker.go")
	content := "// Copyright 2026 Decker.\npackage probe\n\n" + marker + "\n"
	if err := os.WriteFile(badFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", badFile, err)
	}

	// Red: a marked file must fail the marker gate.
	out, err := runGate(t, repoRoot, "^TestNoUpstreamLineageMarkers$")
	assertGateRed(t, out, err, "marker gate", "TestNoUpstreamLineageMarkers")

	// Green: after removal the same command passes.
	if err := os.Remove(badFile); err != nil {
		t.Fatalf("Remove %s: %v", badFile, err)
	}
	if out, err := runGate(t, repoRoot, "^TestNoUpstreamLineageMarkers$"); err != nil {
		t.Errorf("marker gate stayed red after the violation was removed: %v\n%s", err, out)
	}
}

// TestCommitGateDetectsViolation proves that the commit-message gate turns
// red on a violating commit message and green again once the message is
// amended to a neutral one. The probe builds a throwaway repository in the
// system temp dir, so the real repository history is never touched.
func TestCommitGateDetectsViolation(t *testing.T) {
	repoRoot := findRepoRoot(t)

	tmpDir, err := os.MkdirTemp("", "lineage-gate-probe-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// A minimal repository: go.mod lets findRepoRoot resolve inside the
	// probe repo; the compiled gate binary is self-contained and does not
	// need this module to build.
	goMod := "module lineage-probe\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "probe.txt"), []byte("probe\n"), 0o644); err != nil {
		t.Fatalf("WriteFile probe.txt: %v", err)
	}
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "checkout", "-b", "main")
	runGit(t, tmpDir, "add", ".")
	runGitCommit(t, tmpDir, "chore: probe baseline")

	gatesBin := filepath.Join(tmpDir, "gates.test")
	buildCmd := exec.Command("go", "test", "-c", "./tests/lineage", "-o", gatesBin)
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -c failed: %v\n%s", err, out)
	}

	// Red: a violating message must fail the commit gate. Assembled at
	// runtime so the term never appears literally in this file. Amend
	// rewrites the message without touching the work tree.
	runGitAmend(t, tmpDir, "sync "+"Due"+"nde")
	out, err := runGateBinary(t, gatesBin, tmpDir)
	assertGateRed(t, out, err, "commit gate", "TestCommitMessagesStayNeutral")

	// Green: amending to a neutral message turns the gate green again.
	runGitAmend(t, tmpDir, "chore: probe neutral")
	if out, err := runGateBinary(t, gatesBin, tmpDir); err != nil {
		t.Errorf("commit gate stayed red after the message was neutralized: %v\n%s", err, out)
	}
}

// assertGateRed fails the probe unless out+err prove the gate actually ran
// and turned red: a nil error means the gate stayed green, and output
// without a "--- FAIL" header naming the gate means the subprocess never
// exercised it (build or environment failure) — never mistake an
// environment failure for a red gate.
func assertGateRed(t *testing.T, out string, err error, gateName, gateTest string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s stayed green with the violation present; expected red", gateName)
		return
	}
	if !strings.Contains(out, "--- FAIL") || !strings.Contains(out, gateTest) {
		t.Fatalf("%s subprocess failed without running %s (build or environment failure):\n%s", gateName, gateTest, out)
	}
}

// withoutGitEnv returns a copy of base with every GIT_* variable removed,
// so git in subprocesses falls back to working-directory discovery instead
// of trusting ambient environment (an exported GIT_DIR would otherwise
// redirect probe repository operations onto the real repository).
func withoutGitEnv(base []string) []string {
	env := make([]string, 0, len(base))
	for _, kv := range base {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// gitEnv returns an environment for git subprocesses that work on dir:
// ambient GIT_* variables are stripped and GIT_DIR is pinned to dir/.git,
// so no environment can ever make the probe touch the real repository.
func gitEnv(dir string) []string {
	return append(withoutGitEnv(os.Environ()), "GIT_DIR="+filepath.Join(dir, ".git"))
}

// runGate runs a single gate test as a subprocess from repoRoot and
// returns its output and error: a non-nil error means the gate turned red,
// nil means it stayed green. -count=1 defeats the go test result cache,
// whose key does not cover the probe fixtures.
func runGate(t *testing.T, repoRoot, gatePattern string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", "test", "-count=1", "-run", gatePattern, "./tests/lineage/")
	cmd.Dir = repoRoot
	cmd.Env = append(withoutGitEnv(os.Environ()), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runGateBinary runs a compiled gate test binary with cwd set to dir and
// returns its output and error.
func runGateBinary(t *testing.T, bin, dir string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, "-test.run", "^TestCommitMessagesStayNeutral$")
	cmd.Dir = dir
	cmd.Env = withoutGitEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func runGitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git",
		"-c", "user.name=Lineage Probe",
		"-c", "user.email=lineage-probe@example.invalid",
		"commit", "-m", msg)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(dir),
		"GIT_AUTHOR_DATE=2026-08-08T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-08-08T00:00:00+00:00")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit %q failed: %v\n%s", msg, err, out)
	}
}

// runGitAmend rewrites the HEAD commit message in dir.
func runGitAmend(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git",
		"-c", "user.name=Lineage Probe",
		"-c", "user.email=lineage-probe@example.invalid",
		"commit", "--amend", "-m", msg)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(dir),
		"GIT_AUTHOR_DATE=2026-08-08T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-08-08T00:00:00+00:00")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit --amend %q failed: %v\n%s", msg, err, out)
	}
}
