// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the table-level effects of the atomic take and the
// eight scoped removal methods. The contract suites only reach the stores
// through the ports and only see records written by the same stores, so they
// cannot tell a real DELETE from a soft delete, never feed a corrupt
// payload, never write rows before migration 000004, never move a record's
// locators before removing by the old value, and an empty-argument short
// circuit on the Go side hides what the SQL would select. Every assertion
// here therefore reads the real tables (count(*) / EXISTS) instead of
// trusting the port's own return value. Gated behind
// WICKET_PG_TEST_DATABASE_URL like the rest of the real-database e2e tests.
package e2e_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scopedTables lists every table a scoped removal method deletes from.
var scopedTables = []string{
	"authorization_codes", "refresh_tokens", "reference_tokens", "device_codes", "user_consents",
}

// tableCount returns the number of rows in table.
func tableCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// handleExists reports whether table holds a row under handle.
func handleExists(t *testing.T, pool *pgxpool.Pool, table, handle string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM "+table+" WHERE handle = $1)", handle).Scan(&ok); err != nil {
		t.Fatalf("probe %s handle %s: %v", table, handle, err)
	}
	return ok
}

// consentExists reports whether user_consents holds the (subject, client) row.
func consentExists(t *testing.T, pool *pgxpool.Pool, subject, client string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM user_consents WHERE subject_id = $1 AND client_id = $2)",
		subject, client).Scan(&ok); err != nil {
		t.Fatalf("probe user_consents (%s, %s): %v", subject, client, err)
	}
	return ok
}

// assertHandles checks that each listed handle is present (want true) or
// gone (want false) in table.
func assertHandles(t *testing.T, pool *pgxpool.Pool, table string, want bool, handles ...string) {
	t.Helper()
	for _, h := range handles {
		if got := handleExists(t, pool, table, h); got != want {
			t.Errorf("%s handle %s present = %v, want %v", table, h, got, want)
		}
	}
}

// assertRemoved checks a scoped removal's return value against want.
func assertRemoved(t *testing.T, what string, n int, err error, want int) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if n != want {
		t.Errorf("%s removed %d, want %d", what, n, want)
	}
}

// TestE2ETakeDeletesRow asserts that a take physically deletes the row (a
// soft delete or a consumed flag would still pass the suite, which can only
// infer removal from a later ErrNotFound), and that a row whose payload
// fails to decode is still deleted while the caller gets a decode error
// that is not ErrNotFound and a nil code.
func TestE2ETakeDeletesRow(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	codes := store.NewAuthorizationCodeStore(pool, discardLogger())

	t.Run("valid payload", func(t *testing.T) {
		ctx := t.Context()
		if err := codes.StoreAuthorizationCode(ctx, "take-ok", &models.AuthorizationCode{
			ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}); err != nil {
			t.Fatalf("StoreAuthorizationCode: %v", err)
		}
		if !handleExists(t, pool, "authorization_codes", "take-ok") {
			t.Fatal("premise: stored code row missing before take")
		}
		code, err := codes.TakeAuthorizationCode(ctx, "take-ok")
		if err != nil {
			t.Fatalf("TakeAuthorizationCode: %v", err)
		}
		if code == nil || code.ClientId != "client-a" {
			t.Fatalf("TakeAuthorizationCode returned %+v, want the stored code", code)
		}
		if handleExists(t, pool, "authorization_codes", "take-ok") {
			t.Error("authorization_codes row still present after take")
		}
	})

	t.Run("corrupt payload", func(t *testing.T) {
		ctx := t.Context()
		if _, err := pool.Exec(ctx,
			`INSERT INTO authorization_codes (handle, payload) VALUES ($1, '{"payload":"!!"}'::jsonb)`,
			"take-bad"); err != nil {
			t.Fatalf("insert corrupt payload row: %v", err)
		}
		code, err := codes.TakeAuthorizationCode(ctx, "take-bad")
		if err == nil {
			t.Fatal("TakeAuthorizationCode on a corrupt payload returned nil error")
		}
		if errors.Is(err, storage.ErrNotFound) {
			t.Errorf("TakeAuthorizationCode on a corrupt payload = %v, must not be ErrNotFound", err)
		}
		if code != nil {
			t.Errorf("TakeAuthorizationCode on a corrupt payload returned %+v, want nil", code)
		}
		if handleExists(t, pool, "authorization_codes", "take-bad") {
			t.Error("corrupt authorization_codes row still present after take; a single-use code must be consumed")
		}
	})
}

// scopedFixture writes, through the stores, rows that the scoped removal
// tests remove from: two targets per method, plus rows of another client /
// session and rows with no client / session that must survive.
//
//	authorization_codes / refresh_tokens / reference_tokens:
//	  <p>-ca1, <p>-ca2  client-a, sess-x    (ByClient(client-a) target)
//	  <p>-sa1, <p>-sa2  client-y, sess-a    (BySession(sess-a) target)
//	  <p>-keep          client-b, sess-b    (survives)
//	  <p>-none          no client / session (survives)
//	device_codes (ClientId is set only because the model carries a client;
//	it is never a removal key, device codes are removed by session only):
//	  dev-sa1, dev-sa2  sess-a              (BySession(sess-a) target)
//	  dev-keep          sess-b              (survives)
//	  dev-none          no session          (survives)
//	user_consents:
//	  (user-1, client-a), (user-2, client-a) (ByClient(client-a) target)
//	  (user-1, client-b)                     (survives)
func scopedFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	type loc struct{ handle, client, session string }
	rows := []loc{
		{"ca1", "client-a", "sess-x"}, {"ca2", "client-a", "sess-x"},
		{"sa1", "client-y", "sess-a"}, {"sa2", "client-y", "sess-a"},
		{"keep", "client-b", "sess-b"}, {"none", "", ""},
	}

	codes := store.NewAuthorizationCodeStore(pool, discardLogger())
	refresh := store.NewRefreshTokenStore(pool, discardLogger())
	refs := store.NewReferenceTokenStore(pool, discardLogger())
	for _, r := range rows {
		must("store code-"+r.handle, codes.StoreAuthorizationCode(ctx, "code-"+r.handle,
			&models.AuthorizationCode{ClientId: r.client, SessionId: r.session, CreationTime: fixedNow, Lifetime: 300}))
		must("store rt-"+r.handle, refresh.StoreRefreshToken(ctx, "rt-"+r.handle,
			&models.RefreshToken{ClientId: r.client, SessionId: r.session, CreationTime: fixedNow, Lifetime: 300}))
		tok := referenceToken(r.client, claim("sub", "user-1"))
		if r.session != "" {
			tok = referenceToken(r.client, claim("sub", "user-1"), claim("sid", r.session))
		}
		must("store ref-"+r.handle, refs.StoreReferenceToken(ctx, "ref-"+r.handle, tok))
	}

	devices := store.NewDeviceFlowStore(pool, discardLogger())
	for _, r := range []loc{{"sa1", "client-a", "sess-a"}, {"sa2", "client-b", "sess-a"},
		{"keep", "client-a", "sess-b"}, {"none", "client-a", ""}} {
		must("store dev-"+r.handle, devices.StoreDeviceAuthorization(ctx, "dev-"+r.handle, "user-"+r.handle,
			&models.DeviceCode{ClientId: r.client, SessionId: r.session, CreationTime: fixedNow, Lifetime: 300}))
	}

	consents := store.NewUserConsentStore(pool, discardLogger())
	for _, c := range [][2]string{{"user-1", "client-a"}, {"user-2", "client-a"}, {"user-1", "client-b"}} {
		must("store consent "+c[0]+"/"+c[1], consents.StoreUserConsent(ctx,
			&models.Consent{SubjectId: c[0], ClientId: c[1], Scopes: []string{"openid"}, CreationTime: fixedNow}))
	}
}

// revert000004AndUp simulates a database holding rows written before
// 000004, exactly as the 4.1 backfill test does: run 000004's own down file,
// drop its schema_migrations record, then Up applies 000004 again and
// backfills the locator columns from the payload.
func revert000004AndUp(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000004_scoped_removal_columns.down.sql"))
	if err != nil {
		t.Fatalf("read 000004 down file: %v", err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("revert 000004: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = '000004'"); err != nil {
		t.Fatalf("unrecord 000004: %v", err)
	}
	if got := columnType(t, pool, "authorization_codes", "client_id"); got != "" {
		t.Fatalf("authorization_codes.client_id still present after revert (type %s)", got)
	}
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("Up after revert: %v", err)
	}
}

// TestE2EScopedRemovalRemovesBackfilledRows asserts that rows written before
// 000004 are found by all eight scoped removal methods once 000004 runs again:
// each returns the exact count, its targets are gone from the table, and
// every other row stays. For the seven grant methods the rows are reached
// through the locator columns 000004 backfills from the payload; the suites
// never write data before a migration, so a backfill that left the columns
// NULL would pass them while silently skipping every pre-existing record.
// user_consents.client_id predates 000004 (which only indexes it), so the
// consent case shows that pre-000004 consents stay removable, not a backfill.
func TestE2EScopedRemovalRemovesBackfilledRows(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	scopedFixture(t, pool)
	revert000004AndUp(t, pool)
	ctx := t.Context()

	codes := store.NewAuthorizationCodeStore(pool, discardLogger())
	refresh := store.NewRefreshTokenStore(pool, discardLogger())
	refs := store.NewReferenceTokenStore(pool, discardLogger())
	devices := store.NewDeviceFlowStore(pool, discardLogger())
	consents := store.NewUserConsentStore(pool, discardLogger())

	type grantFamily struct {
		table, prefix       string
		byClient, bySession func(context.Context, string) (int, error)
	}
	for _, g := range []grantFamily{
		{"authorization_codes", "code-", codes.RemoveAuthorizationCodesByClient, codes.RemoveAuthorizationCodesBySession},
		{"refresh_tokens", "rt-", refresh.RemoveRefreshTokensByClient, refresh.RemoveRefreshTokensBySession},
		{"reference_tokens", "ref-", refs.RemoveReferenceTokensByClient, refs.RemoveReferenceTokensBySession},
	} {
		n, err := g.byClient(ctx, "client-a")
		assertRemoved(t, g.table+" by client client-a", n, err, 2)
		assertHandles(t, pool, g.table, false, g.prefix+"ca1", g.prefix+"ca2")
		assertHandles(t, pool, g.table, true, g.prefix+"sa1", g.prefix+"sa2", g.prefix+"keep", g.prefix+"none")

		n, err = g.bySession(ctx, "sess-a")
		assertRemoved(t, g.table+" by session sess-a", n, err, 2)
		assertHandles(t, pool, g.table, false, g.prefix+"sa1", g.prefix+"sa2")
		assertHandles(t, pool, g.table, true, g.prefix+"keep", g.prefix+"none")
		if got := tableCount(t, pool, g.table); got != 2 {
			t.Errorf("%s holds %d rows after removals, want 2", g.table, got)
		}
	}

	n, err := devices.RemoveDeviceCodesBySession(ctx, "sess-a")
	assertRemoved(t, "device_codes by session sess-a", n, err, 2)
	assertHandles(t, pool, "device_codes", false, "dev-sa1", "dev-sa2")
	assertHandles(t, pool, "device_codes", true, "dev-keep", "dev-none")
	if got := tableCount(t, pool, "device_codes"); got != 2 {
		t.Errorf("device_codes holds %d rows after removal, want 2", got)
	}

	n, err = consents.RemoveUserConsentsByClient(ctx, "client-a")
	assertRemoved(t, "user_consents by client client-a", n, err, 2)
	for _, subject := range []string{"user-1", "user-2"} {
		if consentExists(t, pool, subject, "client-a") {
			t.Errorf("user_consents (%s, client-a) still present after removal", subject)
		}
	}
	if !consentExists(t, pool, "user-1", "client-b") {
		t.Error("user_consents (user-1, client-b) was removed; only client-a consents may go")
	}
}

// TestE2EScopedRemovalAfterLocatorUpdate asserts that once UpdateRefreshToken
// moves a token from client-a / sess-a to client-b / sess-b, removal by the
// old values deletes nothing and removal by the new values deletes it. The
// suites never remove by a locator after changing it, so a stale column
// would leave the token reachable from neither its old nor its new scope.
func TestE2EScopedRemovalAfterLocatorUpdate(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	refresh := store.NewRefreshTokenStore(pool, discardLogger())

	// store1 takes the subtest's own t: a Fatalf on the parent t from inside
	// t.Run would be reported as a FailNow on a parent test.
	store1 := func(t *testing.T, handle string) {
		t.Helper()
		if err := refresh.StoreRefreshToken(ctx, handle, &models.RefreshToken{
			ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}); err != nil {
			t.Fatalf("StoreRefreshToken %s: %v", handle, err)
		}
		if err := refresh.UpdateRefreshToken(ctx, handle, &models.RefreshToken{
			ClientId: "client-b", SessionId: "sess-b", CreationTime: fixedNow, Lifetime: 300}, 1); err != nil {
			t.Fatalf("UpdateRefreshToken %s: %v", handle, err)
		}
	}

	t.Run("by client", func(t *testing.T) {
		store1(t, "rt-moved-client")
		n, err := refresh.RemoveRefreshTokensByClient(ctx, "client-a")
		assertRemoved(t, "refresh_tokens by old client client-a", n, err, 0)
		assertHandles(t, pool, "refresh_tokens", true, "rt-moved-client")
		n, err = refresh.RemoveRefreshTokensByClient(ctx, "client-b")
		assertRemoved(t, "refresh_tokens by new client client-b", n, err, 1)
		assertHandles(t, pool, "refresh_tokens", false, "rt-moved-client")
	})

	t.Run("by session", func(t *testing.T) {
		store1(t, "rt-moved-session")
		n, err := refresh.RemoveRefreshTokensBySession(ctx, "sess-a")
		assertRemoved(t, "refresh_tokens by old session sess-a", n, err, 0)
		assertHandles(t, pool, "refresh_tokens", true, "rt-moved-session")
		n, err = refresh.RemoveRefreshTokensBySession(ctx, "sess-b")
		assertRemoved(t, "refresh_tokens by new session sess-b", n, err, 1)
		assertHandles(t, pool, "refresh_tokens", false, "rt-moved-session")
	})
}

// TestE2EScopedRemovalEmptyArgumentLeavesRows asserts that every scoped
// removal called with "" returns (0, nil) and leaves every table untouched,
// with rows present that have no client / session (NULL columns) and a
// consent whose client_id is the empty string (user_consents.client_id is
// NOT NULL, so only the Go-side short circuit keeps it out of reach). The
// suites only re-read their own record through Get, so they cannot see
// whether the empty argument reached the SQL and selected such rows.
func TestE2EScopedRemovalEmptyArgumentLeavesRows(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	scopedFixture(t, pool)
	ctx := t.Context()

	consents := store.NewUserConsentStore(pool, discardLogger())
	if err := consents.StoreUserConsent(ctx, &models.Consent{
		SubjectId: "user-3", ClientId: "", Scopes: []string{"openid"}, CreationTime: fixedNow}); err != nil {
		t.Fatalf("StoreUserConsent with empty client: %v", err)
	}
	if !consentExists(t, pool, "user-3", "") {
		t.Fatal("premise: consent with client_id = '' not stored as the empty string")
	}
	for _, table := range []string{"authorization_codes", "refresh_tokens", "reference_tokens"} {
		c, s := locatorColumns(t, pool, table, "client_id", map[string]string{
			"authorization_codes": "code-none", "refresh_tokens": "rt-none", "reference_tokens": "ref-none"}[table])
		if c != nil || s != nil {
			t.Fatalf("premise: %s row without client / session has non-NULL locators", table)
		}
	}
	if _, s := locatorColumns(t, pool, "device_codes", "NULL", "dev-none"); s != nil {
		t.Fatal("premise: device_codes row without session has non-NULL session_id")
	}

	before := make(map[string]int, len(scopedTables))
	for _, table := range scopedTables {
		before[table] = tableCount(t, pool, table)
	}

	codes := store.NewAuthorizationCodeStore(pool, discardLogger())
	refresh := store.NewRefreshTokenStore(pool, discardLogger())
	refs := store.NewReferenceTokenStore(pool, discardLogger())
	devices := store.NewDeviceFlowStore(pool, discardLogger())
	for name, remove := range map[string]func(context.Context, string) (int, error){
		"RemoveAuthorizationCodesByClient":  codes.RemoveAuthorizationCodesByClient,
		"RemoveAuthorizationCodesBySession": codes.RemoveAuthorizationCodesBySession,
		"RemoveRefreshTokensByClient":       refresh.RemoveRefreshTokensByClient,
		"RemoveRefreshTokensBySession":      refresh.RemoveRefreshTokensBySession,
		"RemoveReferenceTokensByClient":     refs.RemoveReferenceTokensByClient,
		"RemoveReferenceTokensBySession":    refs.RemoveReferenceTokensBySession,
		"RemoveDeviceCodesBySession":        devices.RemoveDeviceCodesBySession,
		"RemoveUserConsentsByClient":        consents.RemoveUserConsentsByClient,
	} {
		n, err := remove(ctx, "")
		assertRemoved(t, name+`("")`, n, err, 0)
	}

	for _, table := range scopedTables {
		if got := tableCount(t, pool, table); got != before[table] {
			t.Errorf("%s holds %d rows after empty-argument removals, want %d", table, got, before[table])
		}
	}
	if !consentExists(t, pool, "user-3", "") {
		t.Error("consent with client_id = '' was removed by an empty-argument removal")
	}
}
