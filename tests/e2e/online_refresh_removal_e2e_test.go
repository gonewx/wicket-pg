// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// insertRawRefreshToken writes a refresh_tokens row directly, wrapping
// modelJSON in the v1 payload container. It stands in for rows the store
// API cannot produce: model JSON written by an earlier release without an
// AuthorizedScopes key, or a container whose payload does not decode.
func insertRawRefreshToken(t *testing.T, pool *pgxpool.Pool, handle, sessionID, encodedModel string) {
	t.Helper()
	container, err := json.Marshal(map[string]any{
		"version": 1, "dataProtected": false, "payload": encodedModel})
	if err != nil {
		t.Fatalf("marshal container: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO refresh_tokens (handle, client_id, session_id, expires_at, version, payload) VALUES ($1, 'client-a', $2, $3, 1, $4)",
		handle, nullIfEmptyString(sessionID), fixedNow, container); err != nil {
		t.Fatalf("insert raw refresh token %s: %v", handle, err)
	}
}

func nullIfEmptyString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func encodeModelJSON(t *testing.T, model map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// TestE2ERemoveOnlineRefreshTokensBySessionRealRows checks the selection on
// rows the conformance suite cannot seed through the port: a row whose model
// JSON has no AuthorizedScopes key at all (online, removed), a raw row whose
// scopes hold offline_access (kept), and a row without a session (NULL
// session_id, never selected), next to store-written online, substring-only
// and offline tokens.
func TestE2ERemoveOnlineRefreshTokensBySessionRealRows(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	refresh := store.NewRefreshTokenStore(pool, discardLogger())

	for handle, seed := range map[string]struct {
		session string
		scopes  []string
	}{
		"rt-online":     {"sess-a", []string{"openid", "profile"}},
		"rt-extended":   {"sess-a", []string{"openid", "offline_access_extended"}},
		"rt-offline":    {"sess-a", []string{"offline_access", "openid"}},
		"rt-other-sess": {"sess-b", []string{"openid"}},
		"rt-no-session": {"", []string{"openid"}},
	} {
		if err := refresh.StoreRefreshToken(ctx, handle, &models.RefreshToken{
			ClientId: "client-a", SessionId: seed.session, AuthorizedScopes: seed.scopes,
			CreationTime: fixedNow, Lifetime: 300}); err != nil {
			t.Fatalf("StoreRefreshToken %s: %v", handle, err)
		}
	}
	insertRawRefreshToken(t, pool, "rt-legacy-nokey", "sess-a",
		encodeModelJSON(t, map[string]any{"ClientId": "client-a", "SessionId": "sess-a"}))
	insertRawRefreshToken(t, pool, "rt-legacy-offline", "sess-a",
		encodeModelJSON(t, map[string]any{"ClientId": "client-a", "SessionId": "sess-a",
			"AuthorizedScopes": []string{"offline_access"}}))

	n, err := refresh.RemoveOnlineRefreshTokensBySession(ctx, "sess-a")
	assertRemoved(t, "online refresh tokens of sess-a", n, err, 3)
	assertHandles(t, pool, "refresh_tokens", false, "rt-online", "rt-extended", "rt-legacy-nokey")
	assertHandles(t, pool, "refresh_tokens", true,
		"rt-offline", "rt-legacy-offline", "rt-other-sess", "rt-no-session")

	n, err = refresh.RemoveOnlineRefreshTokensBySession(ctx, "sess-a")
	assertRemoved(t, "second removal for sess-a", n, err, 0)
}

// TestE2ERemoveOnlineRefreshTokensAfterScopeUpdate asserts that the removal
// judges the scopes of the current stored version: UpdateRefreshToken
// rewrites the payload, so a token updated from offline to online is
// removed and one updated from online to offline is kept.
func TestE2ERemoveOnlineRefreshTokensAfterScopeUpdate(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	refresh := store.NewRefreshTokenStore(pool, discardLogger())

	storeThenUpdate := func(handle string, before, after []string) {
		t.Helper()
		if err := refresh.StoreRefreshToken(ctx, handle, &models.RefreshToken{
			ClientId: "client-a", SessionId: "sess-a", AuthorizedScopes: before,
			CreationTime: fixedNow, Lifetime: 300}); err != nil {
			t.Fatalf("StoreRefreshToken %s: %v", handle, err)
		}
		if err := refresh.UpdateRefreshToken(ctx, handle, &models.RefreshToken{
			ClientId: "client-a", SessionId: "sess-a", AuthorizedScopes: after,
			CreationTime: fixedNow, Lifetime: 300}, 1); err != nil {
			t.Fatalf("UpdateRefreshToken %s: %v", handle, err)
		}
	}
	storeThenUpdate("rt-became-online", []string{"openid", "offline_access"}, []string{"openid"})
	storeThenUpdate("rt-became-offline", []string{"openid"}, []string{"openid", "offline_access"})

	n, err := refresh.RemoveOnlineRefreshTokensBySession(ctx, "sess-a")
	assertRemoved(t, "online refresh tokens of sess-a after updates", n, err, 1)
	assertHandles(t, pool, "refresh_tokens", false, "rt-became-online")
	assertHandles(t, pool, "refresh_tokens", true, "rt-became-offline")
}

// TestE2ERemoveOnlineRefreshTokensUndecodablePayloadRemovesNothing asserts
// that the removal is atomic: when one row of the session carries a payload
// that does not decode, the transaction rolls back, the call returns an
// error and no row of the session is removed, not even a decodable online
// one.
func TestE2ERemoveOnlineRefreshTokensUndecodablePayloadRemovesNothing(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	refresh := store.NewRefreshTokenStore(pool, discardLogger())

	if err := refresh.StoreRefreshToken(ctx, "rt-online", &models.RefreshToken{
		ClientId: "client-a", SessionId: "sess-a", AuthorizedScopes: []string{"openid"},
		CreationTime: fixedNow, Lifetime: 300}); err != nil {
		t.Fatalf("StoreRefreshToken: %v", err)
	}
	insertRawRefreshToken(t, pool, "rt-corrupt", "sess-a", "!!not base64!!")

	n, err := refresh.RemoveOnlineRefreshTokensBySession(ctx, "sess-a")
	if err == nil {
		t.Fatalf("RemoveOnlineRefreshTokensBySession over a corrupt payload = (%d, nil), want an error", n)
	}
	if n != 0 {
		t.Errorf("count on error = %d, want 0", n)
	}
	assertHandles(t, pool, "refresh_tokens", true, "rt-online", "rt-corrupt")
}

// TestE2ERemoveOnlineRefreshTokensWithNulInScopes asserts that a token
// whose scopes hold a NUL character, which the store codec writes and reads
// back as the JSON escape \u0000, neither blocks the removal for its
// session nor is misjudged: PostgreSQL's jsonb rejects that escape, so the
// scopes must be judged on the Go side. The online NUL token is removed
// and the offline one is kept.
func TestE2ERemoveOnlineRefreshTokensWithNulInScopes(t *testing.T) {
	pool := newScratchPool(t)
	if err := migrations.Up(t.Context(), pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	ctx := t.Context()
	refresh := store.NewRefreshTokenStore(pool, discardLogger())

	for handle, scopes := range map[string][]string{
		"rt-nul-online":  {"openid", "a\x00b"},
		"rt-nul-offline": {"offline_access", "a\x00b"},
		"rt-plain":       {"openid"},
	} {
		if err := refresh.StoreRefreshToken(ctx, handle, &models.RefreshToken{
			ClientId: "client-a", SessionId: "sess-a", AuthorizedScopes: scopes,
			CreationTime: fixedNow, Lifetime: 300}); err != nil {
			t.Fatalf("StoreRefreshToken %s: %v", handle, err)
		}
	}
	if _, err := refresh.GetRefreshToken(ctx, "rt-nul-online"); err != nil {
		t.Fatalf("premise: token with NUL in scopes not readable: %v", err)
	}

	n, err := refresh.RemoveOnlineRefreshTokensBySession(ctx, "sess-a")
	assertRemoved(t, "online refresh tokens of sess-a with NUL scopes", n, err, 2)
	assertHandles(t, pool, "refresh_tokens", false, "rt-nul-online", "rt-plain")
	assertHandles(t, pool, "refresh_tokens", true, "rt-nul-offline")
}
