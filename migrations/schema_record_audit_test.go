// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package migrations audits the schema design record
// (../docs/schema-design-record.md) against the migration DDL and the store
// sources: every table and every index in the migration SQL must be listed
// in the record, every object the record lists must exist in the migration
// SQL, and every store method the record names must exist in the store
// sources. Pure static file scans — no database, must run in every
// environment (AC-5 of story 1.15).
package migrations

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	tableRe  = regexp.MustCompile(`CREATE TABLE (?:IF NOT EXISTS )?(\w+)`)
	indexRe  = regexp.MustCompile(`CREATE (?:UNIQUE )?INDEX (\w+)`)
	rowColRe = regexp.MustCompile(`^\| ` + "`" + `([a-z_]+)` + "`" + ` \|`)
	methodRe = regexp.MustCompile(`func \(s \*(\w+Store)\) ([A-Z]\w*)\(`)
	refRe    = regexp.MustCompile(`([A-Za-z]+Store)\.([A-Za-z]+)`)
)

// readSQLObjectNames extracts the table and index names from every embedded
// migration file.
func readSQLObjectNames(t *testing.T) (tables, indexes map[string]bool) {
	t.Helper()
	tables = map[string]bool{}
	indexes = map[string]bool{}
	for _, name := range []string{
		"000001_init.up.sql",
		"000002_device_flow_user_code.up.sql",
		"000003_session_subject_id.up.sql",
	} {
		body, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range tableRe.FindAllStringSubmatch(string(body), -1) {
			tables[m[1]] = true
		}
		for _, m := range indexRe.FindAllStringSubmatch(string(body), -1) {
			indexes[m[1]] = true
		}
	}
	return tables, indexes
}

// readRecordRowColumns extracts the first column of every table row in the
// design record (the object-name column).
func readRecordRowColumns(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "docs", "schema-design-record.md"))
	if err != nil {
		t.Fatalf("read docs/schema-design-record.md: %v", err)
	}
	var cols []string
	for _, line := range strings.Split(string(body), "\n") {
		if m := rowColRe.FindStringSubmatch(line); m != nil {
			cols = append(cols, m[1])
		}
	}
	return cols
}

// TestSchemaRecordCoversMigrationObjects asserts the two-way object audit:
// every table and index in the migration SQL appears as an object-name
// column in the design record, and every object-name column in the record
// exists in the migration SQL (no invented tables or indexes).
func TestSchemaRecordCoversMigrationObjects(t *testing.T) {
	tables, indexes := readSQLObjectNames(t)
	if len(tables) == 0 || len(indexes) == 0 {
		t.Fatal("no tables or indexes found in migration SQL")
	}
	cols := readRecordRowColumns(t)
	if len(cols) == 0 {
		t.Fatal("no object-name columns found in design record")
	}

	recorded := map[string]bool{}
	for _, c := range cols {
		if recorded[c] {
			continue
		}
		recorded[c] = true
		if strings.HasPrefix(c, "idx_") {
			if !indexes[c] {
				t.Errorf("record lists index %q not present in migration SQL", c)
			}
		} else if !tables[c] {
			t.Errorf("record lists table %q not present in migration SQL", c)
		}
	}

	for table := range tables {
		if !recorded[table] {
			t.Errorf("migration table %q missing from design record", table)
		}
	}
	for index := range indexes {
		if !recorded[index] {
			t.Errorf("migration index %q missing from design record", index)
		}
	}
}

// TestSchemaRecordStoreMethods asserts that every public store method the
// record names actually exists in the store sources: the explicit
// "XxxStore.Method" references in the record, and conversely every store
// method (except ConformsTo) is named in the record.
func TestSchemaRecordStoreMethods(t *testing.T) {
	record, err := os.ReadFile(filepath.Join("..", "docs", "schema-design-record.md"))
	if err != nil {
		t.Fatalf("read docs/schema-design-record.md: %v", err)
	}
	recordSrc := string(record)

	storeSrc := map[string]string{}
	files, err := filepath.Glob(filepath.Join("..", "store", "*.go"))
	if err != nil {
		t.Fatalf("list store sources: %v", err)
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		storeSrc[filepath.Base(file)] = string(body)
	}
	allStoreSrc := strings.Join(mapValues(storeSrc), "\n")

	methods := map[string]bool{}
	for _, m := range methodRe.FindAllStringSubmatch(allStoreSrc, -1) {
		if m[2] == "ConformsTo" {
			continue
		}
		methods[m[2]] = true
	}
	if len(methods) == 0 {
		t.Fatal("no store methods found")
	}

	// Every store method must be named in the record, as a whole word (so
	// "Store" inside "StoreAuthorizationCode" does not satisfy "Store").
	for method := range methods {
		if !wordIn(method, recordSrc) {
			t.Errorf("store method %q missing from design record", method)
		}
	}

	// Every "XxxStore.Method" reference in the record must exist in the
	// corresponding store source.
	for _, m := range refRe.FindAllStringSubmatch(recordSrc, -1) {
		full := m[1] + "." + m[2]
		storeFile := ""
		for file, src := range storeSrc {
			if strings.Contains(src, "type "+m[1]+" struct") {
				storeFile = file
				break
			}
		}
		if storeFile == "" {
			t.Errorf("record references %s, but no such store type exists", m[1])
			continue
		}
		if !strings.Contains(storeSrc[storeFile], "func (s *"+m[1]+") "+m[2]+"(") {
			t.Errorf("record references %s, but method does not exist", full)
		}
	}
}

// wordIn reports whether s appears in content delimited by non-word
// characters on both sides.
func wordIn(s, content string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(s) + `\b`)
	return re.MatchString(content)
}

func mapValues[V any](m map[string]V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
