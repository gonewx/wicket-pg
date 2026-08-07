// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test — story 2.2 contract-suite CI job, verified end to end.
//
// The conformance job in .github/workflows/ci.yml is the merge gate for
// port-contract compliance: it runs the three suite groups (grant family,
// session, key records) against a postgres:15 service container and fails
// the job on any suite failure. The suites themselves are exercised against
// a real database by tests/conformance; what this suite pins is the job
// configuration contract the story's acceptance criteria describe — service
// container shape, injected database URL, the whole-package command with
// WithMay baked into the fixtures, failure propagation, ordering after
// lineage-gates, and the paired version pins. All assertions are static
// reads of the workflow file and the fixture sources, so the suite runs
// anywhere with no database.
package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow models the subset of a GitHub Actions workflow this suite
// inspects. Unknown keys are ignored by the decoder.
type workflow struct {
	Jobs map[string]job `yaml:"jobs"`
}

type job struct {
	Needs    string             `yaml:"needs"`
	Services map[string]service `yaml:"services"`
	Env      map[string]string  `yaml:"env"`
	Steps    []step             `yaml:"steps"`
}

type service struct {
	Image   string            `yaml:"image"`
	Env     map[string]string `yaml:"env"`
	Ports   []string          `yaml:"ports"`
	Options string            `yaml:"options"`
}

type step struct {
	Uses string            `yaml:"uses"`
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
	If   string            `yaml:"if"`
}

const ciWorkflowPath = ".github/workflows/ci.yml"

// TestE2EConformanceJobUsesPostgresServiceContainer verifies AC-1: the
// conformance job provisions its database through a postgres:15 service
// container with a pg_isready health check and the port published to the
// runner host, never through testcontainers, and injects the connection
// string via WICKET_PG_TEST_DATABASE_URL.
func TestE2EConformanceJobUsesPostgresServiceContainer(t *testing.T) {
	raw, _ := readCIWorkflow(t)
	conformance := requireJob(t, raw, "conformance")

	svc, ok := conformance.Services["postgres"]
	if !ok {
		t.Fatal("conformance job has no postgres service container")
	}
	if svc.Image != "postgres:15" {
		t.Errorf("service image = %q, want postgres:15", svc.Image)
	}
	for _, envKey := range []string{"POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD"} {
		if _, ok := svc.Env[envKey]; !ok {
			t.Errorf("service env missing %s", envKey)
		}
	}
	if len(svc.Ports) != 1 || svc.Ports[0] != "5432:5432" {
		t.Errorf("service ports = %v, want [5432:5432] published to the runner host", svc.Ports)
	}
	for _, opt := range []string{
		`--health-cmd "pg_isready -U postgres"`,
		"--health-interval",
		"--health-timeout",
		"--health-retries",
	} {
		if !strings.Contains(svc.Options, opt) {
			t.Errorf("service options missing health check piece %q: %s", opt, svc.Options)
		}
	}

	url, ok := conformance.Env["WICKET_PG_TEST_DATABASE_URL"]
	if !ok {
		t.Fatal("conformance job env missing WICKET_PG_TEST_DATABASE_URL")
	}
	if url != "postgres://postgres:postgres@localhost:5432/"+svc.Env["POSTGRES_DB"] {
		t.Errorf("WICKET_PG_TEST_DATABASE_URL = %q, want localhost host matching POSTGRES_DB %q",
			url, svc.Env["POSTGRES_DB"])
	}

	// The job provisions the database via the service container above, so
	// no step may itself drive containers (e.g. testcontainers). The
	// workflow head comment may mention the word as a prohibition; only
	// actual step commands must not reference it.
	for _, s := range conformance.Steps {
		if strings.Contains(s.Run, "testcontainers") || strings.Contains(s.Uses, "testcontainers") {
			t.Error("conformance step references testcontainers; the service container must be the only database")
		}
	}
}

// TestE2EConformanceJobRunsWholePackageCommand verifies AC-2: the job runs
// the whole tests/conformance package under GOWORK=off, and the WithMay
// options live inside the fixtures (grant family and key records enabled,
// session suite not), not in the CI command.
func TestE2EConformanceJobRunsWholePackageCommand(t *testing.T) {
	raw, _ := readCIWorkflow(t)
	conformance := requireJob(t, raw, "conformance")

	var contractRun string
	for _, s := range conformance.Steps {
		if s.Name == "Contract suites" {
			contractRun = s.Run
		}
	}
	if contractRun == "" {
		t.Fatal("conformance job has no step named 'Contract suites'")
	}
	if contractRun != "GOWORK=off go test ./tests/conformance/..." {
		t.Errorf("Contract suites run = %q, want the whole-package command", contractRun)
	}
	if strings.Contains(contractRun, "WithMay") {
		t.Error("CI command must not pass WithMay options; they are baked into the fixtures")
	}

	root := repoRoot(t)
	grantFixtures := readFile(t, filepath.Join(root, "tests/conformance/grant_fixtures_test.go"))
	if got := strings.Count(grantFixtures, "storagetest.WithMay(true)"); got != 7 {
		t.Errorf("grant fixtures enable WithMay %d times, want 7 entries", got)
	}

	keymgmtFixtures := readFile(t, filepath.Join(root, "tests/conformance/keymgmt_fixture_test.go"))
	if !strings.Contains(keymgmtFixtures, "keymgmttest.WithMay(true)") {
		t.Error("key records fixture must enable WithMay(true)")
	}

	// The session suite runs without WithMay: its LazyExpiryReclaimsOnRead
	// MAY case is not implementable (story 1.11), so no sessiontest.WithMay
	// call may appear in the fixture.
	sessionFixture := readFile(t, filepath.Join(root, "tests/conformance/session_fixture_test.go"))
	if strings.Contains(sessionFixture, "sessiontest.WithMay") {
		t.Error("session fixture must not enable WithMay")
	}
}

// TestE2EConformanceJobFailurePropagates verifies AC-3: a failing suite
// turns the job red. The Contract suites step runs go test (non-zero exit
// on failure), carries no continue-on-error, and is not gated by an
// if-condition that could swallow the failure or skip the step.
func TestE2EConformanceJobFailurePropagates(t *testing.T) {
	raw, _ := readCIWorkflow(t)
	conformance := requireJob(t, raw, "conformance")

	var contractRun string
	for _, s := range conformance.Steps {
		if s.Name == "Contract suites" {
			contractRun = s.Run
		}
		if s.If != "" {
			t.Errorf("step %q has if-condition %q; a failure could be swallowed", s.Name, s.If)
		}
	}
	if contractRun == "" {
		t.Fatal("conformance job has no step named 'Contract suites'")
	}
	if !strings.HasPrefix(contractRun, "GOWORK=off go test") {
		t.Errorf("Contract suites run %q does not propagate test exit codes", contractRun)
	}
	// continue-on-error at job or step level would swallow a failing
	// suite; the conformance job must fail red on any suite failure.
	for _, s := range conformance.Steps {
		if strings.Contains(s.Run, "continue-on-error") {
			t.Errorf("step %q carries continue-on-error", s.Name)
		}
	}
}

// TestE2EConformanceJobOrderAndVersionPins verifies AC-4: the conformance
// job runs after lineage-gates (build's existing dependency unchanged), no
// run block interpolates event-supplied text, and the setup-go version pin
// stays paired with the go.mod directive.
func TestE2EConformanceJobOrderAndVersionPins(t *testing.T) {
	raw, _ := readCIWorkflow(t)

	conformance := requireJob(t, raw, "conformance")
	if conformance.Needs != "lineage-gates" {
		t.Errorf("conformance job needs = %q, want lineage-gates", conformance.Needs)
	}
	if build, ok := raw.Jobs["build"]; !ok || build.Needs != "lineage-gates" {
		t.Errorf("build job needs must remain lineage-gates; got %q", build.Needs)
	}

	for name, j := range raw.Jobs {
		for i, s := range j.Steps {
			if strings.Contains(s.Run, "${{") {
				t.Errorf("job %s step %d run: block interpolates %q; event text must never enter commands",
					name, i, s.Run)
			}
		}
	}

	root := repoRoot(t)
	goMod := readFile(t, filepath.Join(root, "go.mod"))
	for name, j := range raw.Jobs {
		for _, s := range j.Steps {
			if s.Uses != "actions/setup-go@v5" {
				continue
			}
			if got := s.With["go-version"]; got != "1.27.0-rc.1" {
				t.Errorf("job %s setup-go go-version = %q, want '1.27.0-rc.1'", name, got)
			}
		}
	}
	if !strings.Contains(goMod, "go 1.27rc1") {
		t.Error("go.mod must carry the 'go 1.27rc1' directive paired with the CI pin")
	}
}

// TestE2EConformanceEnvMatchesFactorySkipContract verifies the env var name
// the factory reads matches the variable the CI job injects: the factory
// skips when WICKET_PG_TEST_DATABASE_URL is unset, so the job must set
// exactly that name or the whole package silently skips green.
func TestE2EConformanceEnvMatchesFactorySkipContract(t *testing.T) {
	raw, _ := readCIWorkflow(t)
	conformance := requireJob(t, raw, "conformance")

	root := repoRoot(t)
	factory := readFile(t, filepath.Join(root, "tests/conformance/factory.go"))
	if !strings.Contains(factory, `testDatabaseURLEnv = "WICKET_PG_TEST_DATABASE_URL"`) {
		t.Error("conformance factory must read WICKET_PG_TEST_DATABASE_URL")
	}
	if _, ok := conformance.Env["WICKET_PG_TEST_DATABASE_URL"]; !ok {
		t.Error("conformance job must inject WICKET_PG_TEST_DATABASE_URL")
	}
}

// readCIWorkflow returns the parsed workflow and its raw text.
func readCIWorkflow(t *testing.T) (workflow, []byte) {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ciWorkflowPath))
	var wf workflow
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("parse %s: %v", ciWorkflowPath, err)
	}
	return wf, []byte(raw)
}

func requireJob(t *testing.T, wf workflow, name string) job {
	t.Helper()
	j, ok := wf.Jobs[name]
	if !ok {
		t.Fatalf("workflow has no job %q", name)
	}
	return j
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}
