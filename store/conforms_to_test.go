// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gonewx/wicket/keymgmt/keymgmttest"
	"github.com/gonewx/wicket/session/sessiontest"
	"github.com/gonewx/wicket/storage/storagetest"
)

// discardLogger returns a logger that drops every record. The stores under
// test never write in ConformsTo, and no test may leak identifiers or
// credentials to the test log.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// conformsToCase is one row of the unified compliance-credential table. The
// store field's interface type is the compile-time assertion of AC-3: every
// store must expose the single-value signature ConformsTo() string, or this
// test does not compile.
type conformsToCase struct {
	name  string
	store interface{ ConformsTo() string }
	want  string
}

// TestAllStoresConformToSuiteVersions covers all nine stores in one
// table-driven pass (AC-1): the seven grant-family stores report the
// storage suite version, the session store the session suite version, and
// the key record store the keymgmt suite version. Each expectation is the
// suite package's exported constant (AC-2), so a suite upgrade propagates
// to this test automatically. ConformsTo never touches the pool, so a nil
// pool with a discard logger is sufficient and no database is required.
func TestAllStoresConformToSuiteVersions(t *testing.T) {
	cases := []conformsToCase{
		{"AuthorizationCodeStore", NewAuthorizationCodeStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"RefreshTokenStore", NewRefreshTokenStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"ReferenceTokenStore", NewReferenceTokenStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"UserConsentStore", NewUserConsentStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"PersistedGrantStore", NewPersistedGrantStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"DeviceFlowStore", NewDeviceFlowStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"BackchannelAuthenticationRequestStore", NewBackchannelAuthenticationRequestStore(nil, discardLogger()), storagetest.SuiteVersion},
		{"SessionStore", NewSessionStore(nil, discardLogger()), sessiontest.SuiteVersion},
		{"KeyRecordStore", NewKeyRecordStore(nil, discardLogger()), keymgmttest.SuiteVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.store.ConformsTo(); got != tc.want {
				t.Errorf("ConformsTo() = %q, want %q", got, tc.want)
			}
		})
	}
}

// conformsToMethodBody captures the body of a ConformsTo method. The audited
// methods are single-line returns; the pattern spans to the first closing
// brace, so any validation logic (comparison, loop, panic) inside the body
// is captured and fails the exact-match assertion below.
var conformsToMethodBody = regexp.MustCompile(`func \([^)]*\) ConformsTo\(\) string \{([^}]*)\}`)

// TestConformsToNoHardcodedVersionLiteral statically audits every store
// source file (AC-2, AC-4): a ConformsTo body must be exactly one
// constant-identifier return — "return <suite package>.SuiteVersion" — with
// no string literal and no hardcoded suite version, which also pins the
// absence of internal cross-suite validation, panics, or startup rejection.
// The version literal is assembled at runtime so this file's own source
// cannot be hit by the audit it performs (the lineage gate's self-hit
// lesson).
func TestConformsToNoHardcodedVersionLiteral(t *testing.T) {
	versionLiteral := "1" + ".0.0"
	expectedBodies := []string{
		"return storagetest.SuiteVersion",
		"return sessiontest.SuiteVersion",
		"return keymgmttest.SuiteVersion",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list store sources: %v", err)
	}
	audited := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		matches := conformsToMethodBody.FindAllStringSubmatch(string(body), -1)
		if len(matches) == 0 {
			continue
		}
		audited++
		for _, m := range matches {
			inner := strings.TrimSpace(m[1])
			if strings.Contains(inner, `"`) {
				t.Errorf("%s: ConformsTo body contains a string literal: %q", file, inner)
			}
			if strings.Contains(inner, versionLiteral) {
				t.Errorf("%s: ConformsTo body hardcodes the suite version %q", file, versionLiteral)
			}
			ok := false
			for _, want := range expectedBodies {
				if inner == want {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s: ConformsTo body must be exactly one constant-identifier return, got %q", file, inner)
			}
		}
	}
	if audited != 9 {
		t.Errorf("expected 9 store files with a ConformsTo method, audited %d", audited)
	}
}
