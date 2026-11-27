// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins migration 000007 against rows written by the
// v0.4.0 schema: device-flow and CIBA rows without a version column read
// back as version 1 and accept a version-guarded update with that version,
// and session rows get their renewed column backfilled from the payload so
// Renewed survives the switch to column-authoritative reads. It also pins
// that TouchSession and RenewSession write only the time columns and never
// rewrite the payload. The suite is gated behind
// WICKET_PG_TEST_DATABASE_URL so the plain test run stays green without a
// database.
package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// legacyPayload builds the adapter's version-1 payload container around the
// JSON of v, the shape every v0.4.0 store wrote.
func legacyPayload(t *testing.T, v any) []byte {
	t.Helper()
	model, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal legacy model: %v", err)
	}
	container, err := json.Marshal(map[string]any{
		"version":       1,
		"dataProtected": false,
		"payload":       base64.StdEncoding.EncodeToString(model),
	})
	if err != nil {
		t.Fatalf("marshal legacy container: %v", err)
	}
	return container
}

// rollBack000007 returns a fully migrated scratch schema to its v0.4.0
// shape: the 000007 down file runs and its bookkeeping row is removed, so
// the next Up applies 000007 to whatever rows were written in between.
func rollBack000007(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	down, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", "000007_version_guards_session_renewed.down.sql"))
	if err != nil {
		t.Fatalf("read 000007 down: %v", err)
	}
	if _, err := pool.Exec(t.Context(), string(down)); err != nil {
		t.Fatalf("apply 000007 down: %v", err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM schema_migrations WHERE version = '000007'"); err != nil {
		t.Fatalf("forget 000007: %v", err)
	}
}

func TestE2EMigration000007UpgradesLegacyRows(t *testing.T) {
	pool := newScratchPool(t)
	ctx := t.Context()
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	rollBack000007(t, pool)

	renewed := fixedNow.Add(-time.Hour)
	expires := fixedAlive
	legacyRows := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO device_codes (handle, user_code, expires_at, payload) VALUES ($1, $2, $3, $4)",
			[]any{"device-legacy", "user-legacy", fixedAlive,
				legacyPayload(t, &models.DeviceCode{ClientId: "client-a", CreationTime: fixedNow, Lifetime: 300})}},
		{"INSERT INTO backchannel_auth_requests (handle, expires_at, payload) VALUES ($1, $2, $3)",
			[]any{"req-legacy", fixedAlive,
				legacyPayload(t, &models.BackchannelAuthenticationRequest{RequestId: "req-legacy", ClientId: "client-a", ExpirationTime: fixedAlive})}},
		{"INSERT INTO sessions (session_id, subject_id, client_ids, expires, payload) VALUES ($1, $2, $3, $4, $5)",
			[]any{"sess-legacy", "u1", []string{"client-a"}, expires,
				legacyPayload(t, &session.Record{SessionID: "sess-legacy", SubjectID: "u1", Created: fixedNow.Add(-2 * time.Hour),
					Renewed: renewed, Expires: expires, ClientIDs: []string{"client-a"}})}},
		{"INSERT INTO sessions (session_id, client_ids, payload) VALUES ($1, $2, $3)",
			[]any{"sess-zero", []string{}, legacyPayload(t, &session.Record{SessionID: "sess-zero"})}},
		// A NUL in a string field makes the model JSON carry \u0000, which a
		// ::jsonb cast rejects; a field that spells out the Renewed key must
		// not be mistaken for it.
		{"INSERT INTO sessions (session_id, client_ids, payload) VALUES ($1, $2, $3)",
			[]any{"sess-nul", []string{}, legacyPayload(t, &session.Record{SessionID: "sess-nul",
				DisplayName: "a\x00b \"Renewed\":\"2001-01-01T00:00:00Z\"", Renewed: renewed})}},
	}
	for _, row := range legacyRows {
		if _, err := pool.Exec(ctx, row.sql, row.args...); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("Up over legacy rows: %v", err)
	}

	devices := store.NewDeviceFlowStore(pool, discardLogger())
	dc, err := devices.FindByUserCode(ctx, "user-legacy")
	if err != nil {
		t.Fatalf("FindByUserCode legacy: %v", err)
	}
	if dc.Version != 1 {
		t.Errorf("legacy device code Version = %d, want 1 from the column default", dc.Version)
	}
	dc.IsAuthorized = true
	if err := devices.UpdateByUserCode(ctx, "user-legacy", dc, dc.Version); err != nil {
		t.Fatalf("UpdateByUserCode legacy with read version: %v", err)
	}
	if dc.Version != 2 {
		t.Errorf("legacy device code Version after update = %d, want 2", dc.Version)
	}

	requests := store.NewBackchannelAuthenticationRequestStore(pool, discardLogger())
	req, err := requests.FindBackchannelAuthenticationRequest(ctx, "req-legacy")
	if err != nil {
		t.Fatalf("FindBackchannelAuthenticationRequest legacy: %v", err)
	}
	if req.Version != 1 {
		t.Errorf("legacy CIBA request Version = %d, want 1 from the column default", req.Version)
	}
	req.Status = models.BackchannelAuthenticationRequestStatusApproved
	if err := requests.UpdateBackchannelAuthenticationRequest(ctx, "req-legacy", req, req.Version); err != nil {
		t.Fatalf("UpdateBackchannelAuthenticationRequest legacy with read version: %v", err)
	}

	sessions := store.NewSessionStore(pool, discardLogger())
	rec, err := sessions.GetSession(ctx, "sess-legacy")
	if err != nil {
		t.Fatalf("GetSession legacy: %v", err)
	}
	if !rec.Renewed.Equal(renewed) {
		t.Errorf("legacy session Renewed = %v, want %v backfilled from the payload", rec.Renewed, renewed)
	}
	if !rec.Expires.Equal(expires) {
		t.Errorf("legacy session Expires = %v, want %v", rec.Expires, expires)
	}
	zero, err := sessions.GetSession(ctx, "sess-zero")
	if err != nil {
		t.Fatalf("GetSession zero: %v", err)
	}
	if !zero.Renewed.IsZero() || !zero.Expires.IsZero() {
		t.Errorf("zero-time session Renewed/Expires = %v/%v, want zero", zero.Renewed, zero.Expires)
	}
	nul, err := sessions.GetSession(ctx, "sess-nul")
	if err != nil {
		t.Fatalf("GetSession nul: %v", err)
	}
	if !nul.Renewed.Equal(renewed) {
		t.Errorf("session with NUL / decoy DisplayName Renewed = %v, want %v", nul.Renewed, renewed)
	}
	var renewedIsNull bool
	if err := pool.QueryRow(ctx,
		"SELECT renewed IS NULL FROM sessions WHERE session_id = 'sess-zero'").Scan(&renewedIsNull); err != nil {
		t.Fatalf("read sess-zero renewed: %v", err)
	}
	if !renewedIsNull {
		t.Error("zero Renewed was backfilled as a non-NULL instant, want NULL")
	}
}

func TestE2ESessionRenewAndTouchLeavePayloadUntouched(t *testing.T) {
	pool := newScratchPool(t)
	ctx := t.Context()
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	sessions := store.NewSessionStore(pool, discardLogger())
	if err := sessions.CreateSession(ctx, &session.Record{SessionID: "s1", SubjectID: "u1",
		Created: fixedNow, Renewed: fixedNow, Expires: fixedAlive}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	payloadOf := func() string {
		t.Helper()
		var p string
		if err := pool.QueryRow(ctx, "SELECT payload::text FROM sessions WHERE session_id = 's1'").Scan(&p); err != nil {
			t.Fatalf("read payload: %v", err)
		}
		return p
	}
	before := payloadOf()

	touched := fixedNow.Add(time.Minute)
	if err := sessions.TouchSession(ctx, "s1", touched); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	renewedAt, newExpires := fixedNow.Add(2*time.Minute), fixedAlive.Add(24*time.Hour)
	got, err := sessions.RenewSession(ctx, "s1", renewedAt, newExpires)
	if err != nil {
		t.Fatalf("RenewSession: %v", err)
	}
	if !got.Renewed.Equal(renewedAt) || !got.Expires.Equal(newExpires) {
		t.Errorf("RenewSession returned Renewed/Expires = %v/%v, want %v/%v", got.Renewed, got.Expires, renewedAt, newExpires)
	}
	if after := payloadOf(); after != before {
		t.Errorf("payload changed by TouchSession/RenewSession:\n before %s\n after  %s", before, after)
	}
}
