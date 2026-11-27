// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the scoped removal locator columns added by
// migration 000004 at the column level: every write path stores the model's
// client and session in client_id / session_id, an empty value is stored as
// NULL, UpdateByUserCode and UpdateRefreshToken refresh the columns so no
// stale value survives, and rows written before 000004 are backfilled from
// the payload with the same values the Go side derives (including the
// reference token's single-sid rule). The contract suites cannot see these
// columns at all, so these are suite coverage gap assertions. The suite is
// gated behind WICKET_PG_TEST_DATABASE_URL so the plain test run stays green
// without a database.
package e2e_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/claims"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// locatorColumns reads client_id and session_id of one row. A table without
// a client_id column (device_codes) is read with clientColumn = "NULL".
func locatorColumns(t *testing.T, pool *pgxpool.Pool, table, clientColumn, handle string) (client, session *string) {
	t.Helper()
	err := pool.QueryRow(t.Context(),
		"SELECT "+clientColumn+", session_id FROM "+table+" WHERE handle = $1", handle).
		Scan(&client, &session)
	if err != nil {
		t.Fatalf("query %s locator columns for %s: %v", table, handle, err)
	}
	return client, session
}

// assertLocator compares a nullable column value with the expected model
// value, where an empty expectation means the column must be NULL.
func assertLocator(t *testing.T, what string, got *string, want string) {
	t.Helper()
	switch {
	case want == "" && got != nil:
		t.Errorf("%s = %q, want NULL", what, *got)
	case want != "" && got == nil:
		t.Errorf("%s = NULL, want %q", what, want)
	case want != "" && *got != want:
		t.Errorf("%s = %q, want %q", what, *got, want)
	}
}

func referenceToken(clientID string, claimList ...*claims.Claim) *models.Token {
	return &models.Token{
		ClientId:     clientID,
		CreationTime: fixedNow,
		Lifetime:     300,
		Claims:       claimList,
	}
}

func claim(typ, value string) *claims.Claim {
	return &claims.Claim{Type: typ, Value: value}
}

// TestE2EScopedRemovalColumnsWritePaths asserts that every insert path
// stores the model's client and session in the locator columns, and that an
// empty session is stored as NULL rather than as an empty string.
func TestE2EScopedRemovalColumnsWritePaths(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()

	codes := store.NewAuthorizationCodeStore(pool, discardLogger())
	if err := codes.StoreAuthorizationCode(ctx, "code-sess", &models.AuthorizationCode{
		ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreAuthorizationCode: %v", err)
	}
	if err := codes.StoreAuthorizationCode(ctx, "code-nosess", &models.AuthorizationCode{
		ClientId: "client-b", CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreAuthorizationCode: %v", err)
	}
	c, s := locatorColumns(t, pool, "authorization_codes", "client_id", "code-sess")
	assertLocator(t, "authorization_codes code-sess client_id", c, "client-a")
	assertLocator(t, "authorization_codes code-sess session_id", s, "sess-a")
	c, s = locatorColumns(t, pool, "authorization_codes", "client_id", "code-nosess")
	assertLocator(t, "authorization_codes code-nosess client_id", c, "client-b")
	assertLocator(t, "authorization_codes code-nosess session_id", s, "")
	if err := codes.StoreAuthorizationCode(ctx, "code-noclient", &models.AuthorizationCode{
		SessionId: "sess-c", CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreAuthorizationCode: %v", err)
	}
	c, s = locatorColumns(t, pool, "authorization_codes", "client_id", "code-noclient")
	assertLocator(t, "authorization_codes code-noclient client_id", c, "")
	assertLocator(t, "authorization_codes code-noclient session_id", s, "sess-c")

	refresh := store.NewRefreshTokenStore(pool, discardLogger())
	if err := refresh.StoreRefreshToken(ctx, "rt-sess", &models.RefreshToken{
		ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreRefreshToken: %v", err)
	}
	c, s = locatorColumns(t, pool, "refresh_tokens", "client_id", "rt-sess")
	assertLocator(t, "refresh_tokens rt-sess client_id", c, "client-a")
	assertLocator(t, "refresh_tokens rt-sess session_id", s, "sess-a")
	if err := refresh.StoreRefreshToken(ctx, "rt-noloc", &models.RefreshToken{
		CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreRefreshToken: %v", err)
	}
	c, s = locatorColumns(t, pool, "refresh_tokens", "client_id", "rt-noloc")
	assertLocator(t, "refresh_tokens rt-noloc client_id", c, "")
	assertLocator(t, "refresh_tokens rt-noloc session_id", s, "")

	refs := store.NewReferenceTokenStore(pool, discardLogger())
	if err := refs.StoreReferenceToken(ctx, "ref-sess",
		referenceToken("client-a", claim("sub", "user-1"), claim("sid", "sess-a"))); err != nil {
		t.Fatalf("StoreReferenceToken: %v", err)
	}
	if err := refs.StoreReferenceToken(ctx, "ref-nosess",
		referenceToken("client-b", claim("sub", "user-1"))); err != nil {
		t.Fatalf("StoreReferenceToken: %v", err)
	}
	c, s = locatorColumns(t, pool, "reference_tokens", "client_id", "ref-sess")
	assertLocator(t, "reference_tokens ref-sess client_id", c, "client-a")
	assertLocator(t, "reference_tokens ref-sess session_id", s, "sess-a")
	c, s = locatorColumns(t, pool, "reference_tokens", "client_id", "ref-nosess")
	assertLocator(t, "reference_tokens ref-nosess client_id", c, "client-b")
	assertLocator(t, "reference_tokens ref-nosess session_id", s, "")
	if err := refs.StoreReferenceToken(ctx, "ref-noclient",
		referenceToken("", claim("sid", "sess-c"))); err != nil {
		t.Fatalf("StoreReferenceToken: %v", err)
	}
	c, s = locatorColumns(t, pool, "reference_tokens", "client_id", "ref-noclient")
	assertLocator(t, "reference_tokens ref-noclient client_id", c, "")
	assertLocator(t, "reference_tokens ref-noclient session_id", s, "sess-c")

	devices := store.NewDeviceFlowStore(pool, discardLogger())
	if err := devices.StoreDeviceAuthorization(ctx, "device-sess", "user-sess", &models.DeviceCode{
		ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreDeviceAuthorization: %v", err)
	}
	_, s = locatorColumns(t, pool, "device_codes", "NULL", "device-sess")
	assertLocator(t, "device_codes device-sess session_id", s, "sess-a")
}

// TestE2EScopedRemovalColumnsDeviceApprovalRefreshesSession asserts that
// UpdateByUserCode — where the approval writes the session — refreshes
// device_codes.session_id from NULL to the approving session.
func TestE2EScopedRemovalColumnsDeviceApprovalRefreshesSession(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	devices := store.NewDeviceFlowStore(pool, discardLogger())

	pending := &models.DeviceCode{ClientId: "client-a", CreationTime: fixedNow, Lifetime: 300}
	if err := devices.StoreDeviceAuthorization(ctx, "device-1", "user-1", pending); err != nil {
		t.Fatalf("StoreDeviceAuthorization: %v", err)
	}
	_, s := locatorColumns(t, pool, "device_codes", "NULL", "device-1")
	assertLocator(t, "device_codes session_id before approval", s, "")

	approved := &models.DeviceCode{ClientId: "client-a", SessionId: "sess-approve",
		IsAuthorized: true, CreationTime: fixedNow, Lifetime: 300}
	if err := devices.UpdateByUserCode(ctx, "user-1", approved, pending.Version); err != nil {
		t.Fatalf("UpdateByUserCode: %v", err)
	}
	_, s = locatorColumns(t, pool, "device_codes", "NULL", "device-1")
	assertLocator(t, "device_codes session_id after approval", s, "sess-approve")
}

// TestE2EScopedRemovalColumnsRefreshUpdateFollowsModel asserts that a
// successful version-guarded update refreshes both locator columns from the
// new model, and that a version conflict leaves them untouched.
func TestE2EScopedRemovalColumnsRefreshUpdateFollowsModel(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	refresh := store.NewRefreshTokenStore(pool, discardLogger())

	token := &models.RefreshToken{ClientId: "client-a", SessionId: "sess-a",
		CreationTime: fixedNow, Lifetime: 300}
	if err := refresh.StoreRefreshToken(ctx, "rt-1", token); err != nil {
		t.Fatalf("StoreRefreshToken: %v", err)
	}

	moved := &models.RefreshToken{ClientId: "client-b", SessionId: "sess-b",
		CreationTime: fixedNow, Lifetime: 300}
	if err := refresh.UpdateRefreshToken(ctx, "rt-1", moved, 1); err != nil {
		t.Fatalf("UpdateRefreshToken: %v", err)
	}
	c, s := locatorColumns(t, pool, "refresh_tokens", "client_id", "rt-1")
	assertLocator(t, "refresh_tokens client_id after update", c, "client-b")
	assertLocator(t, "refresh_tokens session_id after update", s, "sess-b")

	stale := &models.RefreshToken{ClientId: "client-c", SessionId: "sess-c",
		CreationTime: fixedNow, Lifetime: 300}
	err := refresh.UpdateRefreshToken(ctx, "rt-1", stale, 1)
	if !errors.Is(err, storage.ErrVersionConflict) {
		t.Fatalf("UpdateRefreshToken with stale version = %v, want ErrVersionConflict", err)
	}
	c, s = locatorColumns(t, pool, "refresh_tokens", "client_id", "rt-1")
	assertLocator(t, "refresh_tokens client_id after conflict", c, "client-b")
	assertLocator(t, "refresh_tokens session_id after conflict", s, "sess-b")

	// Clearing the client and session in the model must clear the columns
	// to NULL, not leave the previous values or store empty strings.
	cleared := &models.RefreshToken{CreationTime: fixedNow, Lifetime: 300}
	if err := refresh.UpdateRefreshToken(ctx, "rt-1", cleared, 2); err != nil {
		t.Fatalf("UpdateRefreshToken clearing locators: %v", err)
	}
	c, s = locatorColumns(t, pool, "refresh_tokens", "client_id", "rt-1")
	assertLocator(t, "refresh_tokens client_id after clearing", c, "")
	assertLocator(t, "refresh_tokens session_id after clearing", s, "")
}

// TestE2EScopedRemovalColumnsBackfillExistingRows simulates a database that
// holds rows written before 000004: records are written through the stores,
// 000004 is reverted by running its own down file (dropping the columns, the
// payload stays), and Up applies 000004 again, which backfills from the
// payload. The backfilled columns must equal what the Go side derives from
// the same records, NULL standing for an empty value.
func TestE2EScopedRemovalColumnsBackfillExistingRows(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()

	codes := store.NewAuthorizationCodeStore(pool, discardLogger())
	refresh := store.NewRefreshTokenStore(pool, discardLogger())
	refs := store.NewReferenceTokenStore(pool, discardLogger())
	devices := store.NewDeviceFlowStore(pool, discardLogger())

	mustNil := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	mustNil("store code-sess", codes.StoreAuthorizationCode(ctx, "code-sess", &models.AuthorizationCode{
		ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}))
	mustNil("store code-nosess", codes.StoreAuthorizationCode(ctx, "code-nosess", &models.AuthorizationCode{
		ClientId: "client-b", CreationTime: fixedNow, Lifetime: 300}))
	mustNil("store rt-sess", refresh.StoreRefreshToken(ctx, "rt-sess", &models.RefreshToken{
		ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}))
	mustNil("store code-noclient", codes.StoreAuthorizationCode(ctx, "code-noclient", &models.AuthorizationCode{
		SessionId: "sess-c", CreationTime: fixedNow, Lifetime: 300}))
	mustNil("store rt-noloc", refresh.StoreRefreshToken(ctx, "rt-noloc", &models.RefreshToken{
		CreationTime: fixedNow, Lifetime: 300}))
	mustNil("store ref-noclient", refs.StoreReferenceToken(ctx, "ref-noclient",
		referenceToken("", claim("sid", "sess-c"))))
	// A sid claim whose value is not a string still counts towards the
	// single-sid rule on the Go side: next to a string sid it makes the
	// session empty, and on its own it yields an empty session too.
	mustNil("store ref-mixed-sid", refs.StoreReferenceToken(ctx, "ref-mixed-sid",
		referenceToken("client-e", claim("sid", "sess-e"), &claims.Claim{Type: "sid", Value: 7.0})))
	mustNil("store ref-nonstring-sid", refs.StoreReferenceToken(ctx, "ref-nonstring-sid",
		referenceToken("client-f", &claims.Claim{Type: "sid", Value: 7.0})))
	mustNil("store ref-one-sid", refs.StoreReferenceToken(ctx, "ref-one-sid",
		referenceToken("client-a", claim("sub", "user-1"), claim("sid", "sess-a"))))
	mustNil("store ref-no-sid", refs.StoreReferenceToken(ctx, "ref-no-sid",
		referenceToken("client-b", claim("sub", "user-1"))))
	mustNil("store ref-two-sid", refs.StoreReferenceToken(ctx, "ref-two-sid",
		referenceToken("client-c", claim("sid", "sess-x"), claim("sid", "sess-y"))))
	mustNil("store ref-nil-claims", refs.StoreReferenceToken(ctx, "ref-nil-claims",
		referenceToken("client-d")))
	mustNil("store device-sess", devices.StoreDeviceAuthorization(ctx, "device-sess", "user-sess",
		&models.DeviceCode{ClientId: "client-a", SessionId: "sess-a", CreationTime: fixedNow, Lifetime: 300}))
	mustNil("store device-nosess", devices.StoreDeviceAuthorization(ctx, "device-nosess", "user-nosess",
		&models.DeviceCode{ClientId: "client-b", CreationTime: fixedNow, Lifetime: 300}))

	// Revert 000004 only: its down file plus its bookkeeping row. The
	// payload column is untouched, exactly as on a pre-000004 database.
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

	for _, handle := range []string{"code-sess", "code-nosess", "code-noclient"} {
		code, err := codes.GetAuthorizationCode(ctx, handle)
		mustNil("get "+handle, err)
		c, s := locatorColumns(t, pool, "authorization_codes", "client_id", handle)
		assertLocator(t, "backfilled authorization_codes "+handle+" client_id", c, code.ClientId)
		assertLocator(t, "backfilled authorization_codes "+handle+" session_id", s, code.SessionId)
	}

	for _, handle := range []string{"rt-sess", "rt-noloc"} {
		token, err := refresh.GetRefreshToken(ctx, handle)
		mustNil("get "+handle, err)
		c, s := locatorColumns(t, pool, "refresh_tokens", "client_id", handle)
		assertLocator(t, "backfilled refresh_tokens "+handle+" client_id", c, token.ClientId)
		assertLocator(t, "backfilled refresh_tokens "+handle+" session_id", s, token.SessionId)
	}

	wantRefSession := map[string]string{
		"ref-one-sid":       "sess-a",
		"ref-no-sid":        "",
		"ref-two-sid":       "",
		"ref-nil-claims":    "",
		"ref-noclient":      "sess-c",
		"ref-mixed-sid":     "",
		"ref-nonstring-sid": "",
	}
	for handle, want := range wantRefSession {
		ref, err := refs.GetReferenceToken(ctx, handle)
		mustNil("get "+handle, err)
		if ref.SessionId() != want {
			t.Fatalf("premise: %s SessionId() = %q, want %q", handle, ref.SessionId(), want)
		}
		c, s := locatorColumns(t, pool, "reference_tokens", "client_id", handle)
		assertLocator(t, "backfilled reference_tokens "+handle+" client_id", c, ref.ClientId)
		assertLocator(t, "backfilled reference_tokens "+handle+" session_id", s, ref.SessionId())
	}

	for _, handle := range []string{"device-sess", "device-nosess"} {
		data, err := devices.FindByDeviceCode(ctx, handle)
		mustNil("find "+handle, err)
		_, s := locatorColumns(t, pool, "device_codes", "NULL", handle)
		assertLocator(t, "backfilled device_codes "+handle+" session_id", s, data.SessionId)
	}
}
