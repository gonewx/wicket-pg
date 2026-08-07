// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test — story 1.15 schema design record, verified end to end
// against a real PostgreSQL server.
//
// The static audit (migrations/schema_record_audit_test.go) proves the
// record agrees with the migration SQL text and the store sources; it cannot
// prove the record agrees with the live database. These tests close that
// gap: every table and every explicit index the record lists must exist as a
// real object after migrations.Up, the index definitions (column order,
// uniqueness, partial predicate) must match the paths the record names, and
// the cleanup/read SQL the record attributes to each index must actually be
// planned through that index (AC-2 of story 1.15: every index backs a real
// path, no invented objects).
//
// Like the story 1.1/1.2 e2e suites, these tests are gated behind
// WICKET_PG_TEST_DATABASE_URL and create a throwaway database per test, so
// the plain test run stays green without a database.
package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// recordObjectRe extracts the object-name column of every table row in the
// design record (the backticked first column), mirroring the static audit's
// rowColRe so both audits parse the record the same way.
var recordObjectRe = regexp.MustCompile(`^\| ` + "`" + `([a-z_]+)` + "`" + ` \|`)

// recordObjectNames reads docs/schema-design-record.md and returns every
// object the record lists, split into tables and explicit indexes.
func recordObjectNames(t *testing.T) (tables, indexes map[string]bool) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "schema-design-record.md"))
	if err != nil {
		t.Fatalf("read docs/schema-design-record.md: %v", err)
	}
	tables = map[string]bool{}
	indexes = map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		m := recordObjectRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.HasPrefix(m[1], "idx_") {
			indexes[m[1]] = true
		} else {
			tables[m[1]] = true
		}
	}
	if len(tables) == 0 || len(indexes) == 0 {
		t.Fatal("no tables or indexes parsed from design record")
	}
	return tables, indexes
}

// TestE2ESchemaRecordMatchesLiveDatabase is the two-way object audit
// against the real database: every table and index the record lists exists
// after Up, and the live object set contains nothing the record omits (the
// auto-created primary-key indexes are the only permitted extras).
func TestE2ESchemaRecordMatchesLiveDatabase(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	recordTables, recordIndexes := recordObjectNames(t)

	for table := range recordTables {
		if !relationExists(t, pool, table) {
			t.Errorf("record lists table %q missing from live database", table)
		}
	}
	for index := range recordIndexes {
		if !indexExists(t, pool, index) {
			t.Errorf("record lists index %q missing from live database", index)
		}
	}

	// Reverse direction: every live object must be in the record. Primary
	// keys are auto-named <table>_pkey and the record covers them in prose
	// (not as explicit index rows), so they are excluded here.
	rows, err := pool.Query(t.Context(),
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("list live tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan live table: %v", err)
		}
		if !recordTables[name] {
			t.Errorf("live table %q missing from design record", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read live tables: %v", err)
	}

	rows, err = pool.Query(t.Context(),
		`SELECT indexname FROM pg_indexes WHERE schemaname = 'public'
		 AND indexname NOT LIKE '%\_pkey'`)
	if err != nil {
		t.Fatalf("list live indexes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan live index: %v", err)
		}
		if !recordIndexes[name] {
			t.Errorf("live index %q missing from design record", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read live indexes: %v", err)
	}
}

// indexDefinition pins one index's live definition: columns in order,
// uniqueness, and the partial predicate (pg_get_expr rendering, which
// normalizes quoting).
type indexDefinition struct {
	columns   []string
	unique    bool
	predicate string // non-empty means a partial index
}

// liveIndexDefinitions reads the live definition of every explicit index.
func liveIndexDefinitions(t *testing.T, pool *pgxpool.Pool) map[string]indexDefinition {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT i.relname,
		        array_agg(a.attname ORDER BY array_position(ix.indkey, a.attnum)),
		        ix.indisunique,
		        COALESCE(pg_get_expr(ix.indpred, ix.indrelid), '')
		 FROM pg_index ix
		 JOIN pg_class i ON i.oid = ix.indexrelid
		 JOIN pg_class c ON c.oid = ix.indrelid
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(ix.indkey)
		 WHERE n.nspname = 'public' AND i.relname LIKE 'idx\_%'
		 GROUP BY i.relname, ix.indisunique, pg_get_expr(ix.indpred, ix.indrelid)`)
	if err != nil {
		t.Fatalf("query live index definitions: %v", err)
	}
	defer rows.Close()

	defs := map[string]indexDefinition{}
	for rows.Next() {
		var (
			name      string
			cols      []string
			unique    bool
			predicate string
		)
		if err := rows.Scan(&name, &cols, &unique, &predicate); err != nil {
			t.Fatalf("scan index definition: %v", err)
		}
		defs[name] = indexDefinition{columns: cols, unique: unique, predicate: predicate}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read index definitions: %v", err)
	}
	return defs
}

// TestE2ESchemaRecordIndexDefinitionsMatchPaths asserts the index
// definitions the record names — column order, uniqueness, the partial
// predicate — hold on the live objects. These are the definitions the
// record's path column depends on (e.g. the subject filter rides the
// (subject_id, client_id, type) prefix, the partial unique index only
// guards non-retired records).
func TestE2ESchemaRecordIndexDefinitionsMatchPaths(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	defs := liveIndexDefinitions(t, pool)

	want := map[string]indexDefinition{
		"idx_authorization_codes_expires_at":       {columns: []string{"expires_at"}},
		"idx_refresh_tokens_expires_at":            {columns: []string{"expires_at"}},
		"idx_reference_tokens_expires_at":          {columns: []string{"expires_at"}},
		"idx_user_consents_expires_at":             {columns: []string{"expires_at"}},
		"idx_persisted_grants_expires_at":          {columns: []string{"expires_at"}},
		"idx_device_codes_expires_at":              {columns: []string{"expires_at"}},
		"idx_backchannel_auth_requests_expires_at": {columns: []string{"expires_at"}},
		"idx_sessions_expires":                     {columns: []string{"expires"}},
		"idx_persisted_grants_subject_id":          {columns: []string{"subject_id", "client_id", "type"}},
		"idx_persisted_grants_session_id":          {columns: []string{"session_id"}},
		"idx_persisted_grants_client_id":           {columns: []string{"client_id", "type"}},
		"idx_persisted_grants_type":                {columns: []string{"type"}},
		"idx_device_codes_user_code":               {columns: []string{"user_code"}, unique: true},
		"idx_sessions_subject_id":                  {columns: []string{"subject_id"}},
		// Partial unique index: public_id is unique only among non-retired
		// records (AD-7), the premise KeyRecordStore.Create relies on.
		"idx_key_records_public_id_unique": {columns: []string{"public_id"}, unique: true, predicate: "phase <> 'retired'"},
	}

	for name, w := range want {
		got, ok := defs[name]
		if !ok {
			t.Errorf("live database has no index %q", name)
			continue
		}
		if len(got.columns) != len(w.columns) {
			t.Errorf("%s columns = %v, want %v", name, got.columns, w.columns)
			continue
		}
		for i := range w.columns {
			if got.columns[i] != w.columns[i] {
				t.Errorf("%s columns = %v, want %v", name, got.columns, w.columns)
				break
			}
		}
		if got.unique != w.unique {
			t.Errorf("%s unique = %v, want %v", name, got.unique, w.unique)
		}
		if w.predicate != "" &&
			(!strings.Contains(got.predicate, "phase") || !strings.Contains(got.predicate, "retired")) {
			t.Errorf("%s predicate = %q, want one mentioning phase/retired", name, got.predicate)
		}
		if w.predicate == "" && got.predicate != "" {
			t.Errorf("%s has unexpected predicate %q", name, got.predicate)
		}
	}
}

// cleanupPath describes one record-declared cleanup path: the table, the
// expiry column (sessions uses "expires", everything else "expires_at"),
// and the index that must serve the DELETE.
type cleanupPath struct {
	table string
	col   string
	index string
}

var cleanupPaths = []cleanupPath{
	{"authorization_codes", "expires_at", "idx_authorization_codes_expires_at"},
	{"refresh_tokens", "expires_at", "idx_refresh_tokens_expires_at"},
	{"reference_tokens", "expires_at", "idx_reference_tokens_expires_at"},
	{"user_consents", "expires_at", "idx_user_consents_expires_at"},
	{"persisted_grants", "expires_at", "idx_persisted_grants_expires_at"},
	{"device_codes", "expires_at", "idx_device_codes_expires_at"},
	{"backchannel_auth_requests", "expires_at", "idx_backchannel_auth_requests_expires_at"},
	{"sessions", "expires", "idx_sessions_expires"},
}

// TestE2ESchemaRecordCleanupPathsHitTheirIndexes verifies the record's
// cleanup-path claim end to end: the unified reclamation DELETE (expiry
// column IS NOT NULL and below the cutoff, AD-5) is planned through the
// index the record names. enable_seqscan is disabled so the planner cannot
// fall back to a sequential scan on the empty scratch tables; EXPLAIN never
// executes the DELETE.
func TestE2ESchemaRecordCleanupPathsHitTheirIndexes(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}

	for _, path := range cleanupPaths {
		query := "EXPLAIN (COSTS OFF) DELETE FROM " + path.table +
			" WHERE " + path.col + ` IS NOT NULL AND ` + path.col + ` < '2099-01-01'`
		if !planUsesIndex(t, conn, query, path.index) {
			t.Errorf("%s cleanup plan does not use %s", path.table, path.index)
		}
	}
}

// planUsesIndex runs EXPLAIN (COSTS OFF) on query and reports whether the
// plan scans through index. EXPLAIN emits one row per plan node, so every
// row is collected before matching; both plain and bitmap/only index scans
// count, as does an Index Only Scan (the shape a covering pkey lookup
// takes).
func planUsesIndex(t *testing.T, conn *pgxpool.Conn, query, index string) bool {
	t.Helper()
	rows, err := conn.Query(t.Context(), query)
	if err != nil {
		t.Fatalf("explain %s: %v", query, err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain row: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read explain rows: %v", err)
	}
	if !strings.Contains(plan.String(), "Index Scan using "+index) &&
		!strings.Contains(plan.String(), "Index Only Scan using "+index) &&
		!strings.Contains(plan.String(), "Bitmap Index Scan on "+index) {
		t.Errorf("plan does not use %s:\n%s", index, plan.String())
		return false
	}
	return true
}

// readPath describes one record-declared read/bulk-revocation path: the
// WHERE filter the store runs and the index that must serve it.
type readPath struct {
	query string
	index string
}

var readPaths = []readPath{
	{"SELECT key FROM persisted_grants WHERE subject_id = 'sub-1'", "idx_persisted_grants_subject_id"},
	{"SELECT key FROM persisted_grants WHERE session_id = 'sess-1'", "idx_persisted_grants_session_id"},
	{"SELECT key FROM persisted_grants WHERE client_id = 'c-1'", "idx_persisted_grants_client_id"},
	{"SELECT key FROM persisted_grants WHERE type = 'oauth2'", "idx_persisted_grants_type"},
	{"SELECT handle FROM device_codes WHERE user_code = 'uc-1'", "idx_device_codes_user_code"},
	{"SELECT session_id FROM sessions WHERE subject_id = 'sub-1'", "idx_sessions_subject_id"},
	{"SELECT handle FROM authorization_codes WHERE handle = 'h-1'", "authorization_codes_pkey"},
	{"SELECT session_id FROM sessions WHERE session_id = 's-1'", "sessions_pkey"},
}

// TestE2ESchemaRecordReadPathsHitTheirIndexes verifies the record's
// read-path claims end to end: each filter the record attributes to an
// index (subject/session/client/type filters, the user_code lookup, and the
// primary-key single-point lookups) is planned through that index.
func TestE2ESchemaRecordReadPathsHitTheirIndexes(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}

	for _, path := range readPaths {
		if !planUsesIndex(t, conn, "EXPLAIN (COSTS OFF) "+path.query, path.index) {
			t.Errorf("%s not planned through %s", path.query, path.index)
		}
	}
}
