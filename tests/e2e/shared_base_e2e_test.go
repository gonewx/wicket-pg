// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test — story 1.2 shared base, verified end to end against a
// real PostgreSQL server.
//
// The shared base (baseStore, payloadCodec, mapDuplicateErr, mapReadErr) is
// deliberately unexported; its unit tests cover the pure logic. What the
// unit tests cannot do is prove that the premises the base relies on hold
// against the real adapter. These tests pin those premises:
//
//   - a genuine unique violation surfaces as *pgconn.PgError with SQLSTATE
//     23505, reachable via errors.As — the premise mapDuplicateErr turns
//     into the port family's duplicate sentinel;
//   - a single-record read that matches nothing surfaces as pgx.ErrNoRows,
//     reachable via errors.Is — the premise mapReadErr turns into the
//     missing sentinel, and a no-match list query succeeds with zero rows
//     (the empty non-nil slice contract);
//   - a payload container in the documented shape {"version":1,
//     "dataProtected":false,"payload":"<base64>"} written to a real jsonb
//     column reads back losslessly — opaque []byte model fields (NUL bytes,
//     invalid UTF-8) and time.Time survive the round trip through the
//     store-shaped read path (scan jsonb into []byte, then decode).
//
// The suite is gated behind WICKET_PG_TEST_DATABASE_URL (an admin database
// whose role can CREATE DATABASE) so the plain test run stays green without
// a database.
package e2e_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Fixed time bases shared by every e2e case, mirroring the conformance
// suite's base so column assertions share its determinism. They live here
// rather than in whichever store file happened to need one first: the same
// three instants were previously re-derived as literals across ten files
// under six different local names, and two of them sat in business-store
// files that other files had to point at by comment.
//
// The adapter never reads a clock — expiry decisions belong to the core with
// an injected clock — so these tests pin explicit instants and assert stored
// columns against them. The ordering fixedExpired < fixedNow < fixedAlive is
// the contract: a cutoff at fixedAlive must reclaim a record built from
// fixedExpired and spare one that expires after it.
var (
	fixedNow      = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	fixedMidnight = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	fixedAlive    = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	fixedExpired  = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
)

// TestE2EDuplicateErrorMappingPremise proves the 23505 premise of
// mapDuplicateErr with genuine constraint violations: a primary-key
// collision on authorization_codes and the partial unique index on
// key_records (public_id among non-retired records). Both must surface as
// *pgconn.PgError with Code "23505" reachable through wrapping layers.
func TestE2EDuplicateErrorMappingPremise(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	const insertCode = `INSERT INTO authorization_codes (handle, expires_at, payload)
		VALUES ($1, NULL, '{"version":1}'::jsonb)`

	if _, err := pool.Exec(t.Context(), insertCode, "dup-handle"); err != nil {
		t.Fatalf("first code insert: %v", err)
	}
	if _, err := pool.Exec(t.Context(), insertCode, "dup-handle"); !isUniqueViolation(t, err) {
		t.Errorf("duplicate primary key error = %v, want SQLSTATE 23505", err)
	}

	const insertRecord = `INSERT INTO key_records (handle, public_id, phase, version, payload)
		VALUES ($1, $2, 'active', 1, '{"version":1}'::jsonb)`

	if _, err := pool.Exec(t.Context(), insertRecord, "rec-1", "pub-1"); err != nil {
		t.Fatalf("first record insert: %v", err)
	}
	// The partial unique index is an index, not a constraint: verify it
	// still reports the same SQLSTATE the mapping helper expects.
	if _, err := pool.Exec(t.Context(), insertRecord, "rec-2", "pub-1"); !isUniqueViolation(t, err) {
		t.Errorf("duplicate public_id error = %v, want SQLSTATE 23505", err)
	}
}

// TestE2ENoRowsErrorMappingPremise proves the pgx.ErrNoRows premise of
// mapReadErr with a real single-record read that matches nothing, and the
// empty-set premise of the list contract with a no-match query.
func TestE2ENoRowsErrorMappingPremise(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var payload []byte
	err := pool.QueryRow(t.Context(),
		"SELECT payload FROM authorization_codes WHERE handle = $1",
		"missing-handle").Scan(&payload)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("missing single-record read error = %v, want pgx.ErrNoRows", err)
	}

	rows, err := pool.Query(t.Context(),
		"SELECT handle FROM authorization_codes WHERE handle = $1",
		"missing-handle")
	if err != nil {
		t.Fatalf("no-match list query: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("no-match list query returned a row")
	}
	if err := rows.Err(); err != nil {
		t.Errorf("no-match list query rows.Err = %v, want nil", err)
	}
}

// TestE2EPayloadContainerJSONBRoundTrip drives the AC-3 container contract
// through the store-shaped storage path: a container built by the fixture
// encoder (the documented encode contract, written out here deliberately so
// this test pins the DB-facing shape) is inserted into a jsonb payload
// column, read back as []byte exactly like a store read will scan it, and
// decoded by the fixture decoder. Opaque bytes and time.Time must survive.
func TestE2EPayloadContainerJSONBRoundTrip(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}

	model := grantModel{
		Data:     []byte{0x00, 0xff, 0x80, 'a', 0x00, 0xfe},
		IssuedAt: time.Now().UTC(),
		Meta:     meta{ClientID: "client-1", Scopes: []string{"openid", "offline"}},
	}
	container, err := encodeContainer(model)
	if err != nil {
		t.Fatalf("encode fixture container: %v", err)
	}

	const insert = `INSERT INTO persisted_grants
		(key, subject_id, session_id, client_id, type, expires_at, payload)
		VALUES ('g-1', 'sub-1', 'sess-1', 'client-1', 'oauth2', NULL, $1::jsonb)`
	if _, err := pool.Exec(t.Context(), insert, container); err != nil {
		t.Fatalf("insert grant with container: %v", err)
	}

	// The store read shape: scan the jsonb column into []byte, decode.
	var stored []byte
	if err := pool.QueryRow(t.Context(),
		"SELECT payload FROM persisted_grants WHERE key = $1", "g-1").Scan(&stored); err != nil {
		t.Fatalf("read payload: %v", err)
	}

	var raw struct {
		Version       int    `json:"version"`
		DataProtected bool   `json:"dataProtected"`
		Payload       string `json:"payload"`
	}
	if err := json.Unmarshal(stored, &raw); err != nil {
		t.Fatalf("stored container is not valid JSON: %v", err)
	}
	if raw.Version != 1 || raw.DataProtected {
		t.Errorf("stored container = version %d, dataProtected %v; want version 1, false", raw.Version, raw.DataProtected)
	}

	modelBytes, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal fixture model: %v", err)
	}
	decodedPayload, err := base64.StdEncoding.DecodeString(raw.Payload)
	if err != nil {
		t.Fatalf("stored payload is not valid base64: %v", err)
	}
	if !bytes.Equal(decodedPayload, modelBytes) {
		t.Errorf("payload through jsonb = %q, want exact model JSON %q", decodedPayload, modelBytes)
	}

	var out grantModel
	if err := json.Unmarshal(decodedPayload, &out); err != nil {
		t.Fatalf("decode stored model: %v", err)
	}
	if !bytes.Equal(out.Data, model.Data) {
		t.Errorf("opaque bytes round trip = %v, want %v", out.Data, model.Data)
	}
	if !out.IssuedAt.Equal(model.IssuedAt) {
		t.Errorf("time round trip = %v, want instant of %v", out.IssuedAt, model.IssuedAt)
	}
	if out.Meta.ClientID != model.Meta.ClientID || len(out.Meta.Scopes) != 2 ||
		out.Meta.Scopes[0] != "openid" || out.Meta.Scopes[1] != "offline" {
		t.Errorf("nested struct round trip = %+v, want %+v", out.Meta, model.Meta)
	}
}

// grantModel and meta mirror the shape of a persisted grant model: an
// opaque []byte field, a timestamp, and a nested struct.
type grantModel struct {
	Data     []byte    `json:"data"`
	IssuedAt time.Time `json:"issued_at"`
	Meta     meta      `json:"meta"`
}

type meta struct {
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

// encodeContainer implements the documented encode contract so this test
// pins the DB-facing container shape independently of the adapter's codec:
// model JSON, base64-encoded into the payload field of the versioned
// container.
func encodeContainer(v any) ([]byte, error) {
	model, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version       int    `json:"version"`
		DataProtected bool   `json:"dataProtected"`
		Payload       string `json:"payload"`
	}{
		Version:       1,
		DataProtected: false,
		Payload:       base64.StdEncoding.EncodeToString(model),
	})
}

// isUniqueViolation reports whether err is a SQLSTATE 23505 error reachable
// through wrapping layers, the exact premise mapDuplicateErr relies on.
func isUniqueViolation(t *testing.T, err error) bool {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("error %v is not a *pgconn.PgError", err)
		return false
	}
	if pgErr.Code != "23505" {
		t.Errorf("error SQLSTATE = %s, want 23505", pgErr.Code)
		return false
	}
	return true
}
