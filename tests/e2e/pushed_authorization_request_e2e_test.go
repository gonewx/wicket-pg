// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the pushed authorization request store's semantics
// that the conformance suite cannot reach, by querying the
// pushed_authorization_requests table directly: the visible columns copied
// at write time (an empty ClientId as NULL), the payload container shape,
// sub-microsecond times surviving the round trip through payload, the
// reclamation of a zero RetentionExpiration, and a take of an undecodable
// row. The suite is gated behind WICKET_PG_TEST_DATABASE_URL so the plain
// test run stays green without a database.
package e2e_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// parKey derives a store key the way the core does, from a request_uri
// built on the port's prefix. Keys are never assembled by hand.
func parKey(t *testing.T, suffix string) string {
	t.Helper()
	key, err := storage.PushedAuthorizationRequestKey(storage.PushedAuthorizationRequestURIPrefix + suffix)
	if err != nil {
		t.Fatalf("derive pushed authorization request key: %v", err)
	}
	return key
}

// newPARStore migrates a scratch database and returns the store with its
// pool, so a test can both drive the port and query the table.
func newPARStore(t *testing.T) (*store.PushedAuthorizationRequestStore, *pgxpool.Pool) {
	t.Helper()
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	return store.NewPushedAuthorizationRequestStore(pool, discardLogger()), pool
}

// TestE2EPushedAuthorizationRequestVisibleColumns pins the write-time copy of
// the visible columns: client_id and the three time columns equal the model
// values.
func TestE2EPushedAuthorizationRequestVisibleColumns(t *testing.T) {
	s, pool := newPARStore(t)
	key := parKey(t, "visible-columns")
	request := &models.PushedAuthorizationRequest{
		ClientId:            "c",
		CreationTime:        fixedNow,
		EntryExpiration:     fixedNow.Add(90 * time.Second),
		RetentionExpiration: fixedNow.Add(30 * time.Minute),
		Payload:             []byte("opaque"),
	}
	if err := s.StorePushedAuthorizationRequest(t.Context(), key, request); err != nil {
		t.Fatalf("StorePushedAuthorizationRequest: %v", err)
	}

	var (
		clientID                   *string
		creation, entry, retention time.Time
	)
	if err := pool.QueryRow(t.Context(),
		"SELECT client_id, creation_time, entry_expiration, retention_expiration FROM pushed_authorization_requests WHERE key = $1",
		key).Scan(&clientID, &creation, &entry, &retention); err != nil {
		t.Fatalf("read visible columns: %v", err)
	}
	if clientID == nil || *clientID != "c" {
		t.Errorf("client_id = %v, want %q", clientID, "c")
	}
	if !creation.Equal(request.CreationTime) {
		t.Errorf("creation_time = %v, want %v", creation, request.CreationTime)
	}
	if !entry.Equal(request.EntryExpiration) {
		t.Errorf("entry_expiration = %v, want %v", entry, request.EntryExpiration)
	}
	if !retention.Equal(request.RetentionExpiration) {
		t.Errorf("retention_expiration = %v, want %v", retention, request.RetentionExpiration)
	}
}

// TestE2EPushedAuthorizationRequestEmptyClientIdStoresNull pins that an empty
// ClientId is written to client_id as NULL, not as an empty string.
func TestE2EPushedAuthorizationRequestEmptyClientIdStoresNull(t *testing.T) {
	s, pool := newPARStore(t)
	key := parKey(t, "empty-client")
	if err := s.StorePushedAuthorizationRequest(t.Context(), key, &models.PushedAuthorizationRequest{
		CreationTime:        fixedNow,
		EntryExpiration:     fixedNow.Add(time.Minute),
		RetentionExpiration: fixedNow.Add(time.Hour),
	}); err != nil {
		t.Fatalf("StorePushedAuthorizationRequest: %v", err)
	}
	var isNull bool
	if err := pool.QueryRow(t.Context(),
		"SELECT client_id IS NULL FROM pushed_authorization_requests WHERE key = $1", key).Scan(&isNull); err != nil {
		t.Fatalf("read client_id: %v", err)
	}
	if !isNull {
		t.Error("client_id for an empty ClientId is not NULL")
	}
}

// TestE2EPushedAuthorizationRequestPayloadShape pins the payload column as
// the versioned container {"version":1,"dataProtected":false,"payload":
// "<base64>"}: exactly those three keys, the payload a base64 string. The
// opaque request bytes carry NUL and invalid UTF-8, which must survive only
// because they travel base64-encoded.
func TestE2EPushedAuthorizationRequestPayloadShape(t *testing.T) {
	s, pool := newPARStore(t)
	key := parKey(t, "payload-shape")
	opaque := []byte{0x00, 0xff, 0xfe, 'a', 0x00, 0xc3, 0x28}
	request := &models.PushedAuthorizationRequest{
		ClientId:            "client-shape",
		CreationTime:        fixedNow,
		EntryExpiration:     fixedNow.Add(time.Minute),
		RetentionExpiration: fixedNow.Add(time.Hour),
		Payload:             opaque,
	}
	if err := s.StorePushedAuthorizationRequest(t.Context(), key, request); err != nil {
		t.Fatalf("StorePushedAuthorizationRequest: %v", err)
	}

	var stored []byte
	if err := pool.QueryRow(t.Context(),
		"SELECT payload FROM pushed_authorization_requests WHERE key = $1", key).Scan(&stored); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stored, &fields); err != nil {
		t.Fatalf("payload is not a JSON object: %v", err)
	}
	if len(fields) != 3 {
		t.Errorf("payload keys = %d (%s), want exactly version, dataProtected, payload", len(fields), stored)
	}
	if string(fields["version"]) != "1" {
		t.Errorf("version = %s, want 1", fields["version"])
	}
	if string(fields["dataProtected"]) != "false" {
		t.Errorf("dataProtected = %s, want false", fields["dataProtected"])
	}
	var encoded string
	if err := json.Unmarshal(fields["payload"], &encoded); err != nil {
		t.Fatalf("payload field is not a JSON string: %v", err)
	}
	modelJSON, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("payload field is not standard base64: %v", err)
	}
	var model models.PushedAuthorizationRequest
	if err := json.Unmarshal(modelJSON, &model); err != nil {
		t.Fatalf("decoded payload is not the model JSON: %v", err)
	}
	if model.ClientId != request.ClientId || !bytes.Equal(model.Payload, opaque) {
		t.Errorf("decoded model = {ClientId:%q Payload:%v}, want {ClientId:%q Payload:%v}",
			model.ClientId, model.Payload, request.ClientId, opaque)
	}

	got, err := s.GetPushedAuthorizationRequest(t.Context(), key)
	if err != nil {
		t.Fatalf("GetPushedAuthorizationRequest: %v", err)
	}
	if !bytes.Equal(got.Payload, opaque) {
		t.Errorf("Get Payload = %v, want %v", got.Payload, opaque)
	}
}

// TestE2EPushedAuthorizationRequestSubMicrosecondTimes pins that the model is
// rebuilt from payload, never from the timestamptz columns: times carrying
// nanoseconds come back Equal to the originals through both Get and Take,
// although the columns keep only microseconds.
func TestE2EPushedAuthorizationRequestSubMicrosecondTimes(t *testing.T) {
	s, pool := newPARStore(t)
	key := parKey(t, "sub-microsecond")
	request := &models.PushedAuthorizationRequest{
		ClientId:            "client-nanos",
		CreationTime:        fixedNow.Add(123456789 * time.Nanosecond),
		EntryExpiration:     fixedNow.Add(90*time.Second + 987654321*time.Nanosecond),
		RetentionExpiration: fixedNow.Add(30*time.Minute + 1*time.Nanosecond),
		Payload:             []byte("nanos"),
	}
	if err := s.StorePushedAuthorizationRequest(t.Context(), key, request); err != nil {
		t.Fatalf("StorePushedAuthorizationRequest: %v", err)
	}

	// The column really is coarser than the model: this is what makes
	// reading the model back from columns lossy.
	var column time.Time
	if err := pool.QueryRow(t.Context(),
		"SELECT creation_time FROM pushed_authorization_requests WHERE key = $1", key).Scan(&column); err != nil {
		t.Fatalf("read creation_time: %v", err)
	}
	if column.Equal(request.CreationTime) {
		t.Fatalf("creation_time column kept nanoseconds (%v); the fixture no longer exercises truncation", column)
	}

	check := func(op string, got *models.PushedAuthorizationRequest) {
		t.Helper()
		if !got.CreationTime.Equal(request.CreationTime) {
			t.Errorf("%s CreationTime = %v, want %v", op, got.CreationTime, request.CreationTime)
		}
		if !got.EntryExpiration.Equal(request.EntryExpiration) {
			t.Errorf("%s EntryExpiration = %v, want %v", op, got.EntryExpiration, request.EntryExpiration)
		}
		if !got.RetentionExpiration.Equal(request.RetentionExpiration) {
			t.Errorf("%s RetentionExpiration = %v, want %v", op, got.RetentionExpiration, request.RetentionExpiration)
		}
	}
	got, err := s.GetPushedAuthorizationRequest(t.Context(), key)
	if err != nil {
		t.Fatalf("GetPushedAuthorizationRequest: %v", err)
	}
	check("Get", got)
	taken, err := s.TakePushedAuthorizationRequest(t.Context(), key)
	if err != nil {
		t.Fatalf("TakePushedAuthorizationRequest: %v", err)
	}
	check("Take", taken)
}

// TestE2EPushedAuthorizationRequestZeroRetentionReclaimed pins that a zero
// RetentionExpiration is stored as-is (not as NULL) and reclaimed by any
// cutoff, with a count of exactly one.
func TestE2EPushedAuthorizationRequestZeroRetentionReclaimed(t *testing.T) {
	s, pool := newPARStore(t)
	zero := parKey(t, "zero-retention")
	if err := s.StorePushedAuthorizationRequest(t.Context(), zero, &models.PushedAuthorizationRequest{
		ClientId: "client-zero",
	}); err != nil {
		t.Fatalf("StorePushedAuthorizationRequest: %v", err)
	}
	var retention time.Time
	if err := pool.QueryRow(t.Context(),
		"SELECT retention_expiration FROM pushed_authorization_requests WHERE key = $1", zero).Scan(&retention); err != nil {
		t.Fatalf("read retention_expiration: %v", err)
	}
	if !retention.Equal(time.Time{}) {
		t.Errorf("retention_expiration = %v, want the zero instant", retention)
	}

	// Any cutoff past the zero instant reclaims the row; the earliest
	// fixture instant is enough.
	n, err := s.RemoveExpired(t.Context(), fixedExpired)
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired removed %d rows, want 1", n)
	}
	if _, err := s.GetPushedAuthorizationRequest(t.Context(), zero); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get after reclaim error = %v, want storage.ErrNotFound", err)
	}
}

// TestE2EPushedAuthorizationRequestTakeUndecodablePayload pins a take of a
// row whose payload cannot be decoded: the caller gets a wrapped decode
// error, not storage.ErrNotFound, and the row is already gone, because the
// DELETE ... RETURNING has committed before decoding.
func TestE2EPushedAuthorizationRequestTakeUndecodablePayload(t *testing.T) {
	s, pool := newPARStore(t)
	key := parKey(t, "undecodable")
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO pushed_authorization_requests
			(key, client_id, creation_time, entry_expiration, retention_expiration, payload)
			VALUES ($1, 'client-bad', $2, $2, $2, '{"version":1,"dataProtected":false,"payload":"!!not-base64!!"}'::jsonb)`,
		key, fixedNow); err != nil {
		t.Fatalf("insert undecodable row: %v", err)
	}

	got, err := s.TakePushedAuthorizationRequest(t.Context(), key)
	if err == nil {
		t.Fatalf("Take of an undecodable row succeeded with %+v", got)
	}
	if errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Take error = %v, want a decode error, not storage.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("Take record = %+v, want nil", got)
	}
	var count int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM pushed_authorization_requests WHERE key = $1", key).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("undecodable row still present after Take (%d rows), want removed", count)
	}
}
