// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package e2e_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// legacyRefreshTokenFilter is the selection query the deployment guide
// (section 6.4) gives hosts for finding refresh tokens written by a wicket
// v0.1.x core: such a token carries a zero CreationTime and a zero Lifetime,
// so the derived expires_at is 0001-01-01 and falls before 0002-01-01.
const legacyRefreshTokenFilter = "SELECT handle FROM refresh_tokens WHERE expires_at < '0002-01-01' ORDER BY handle"

// TestE2ELegacyRefreshTokenBackfillSurvivesRemoveExpired pins the hazard
// the v0.2.1 upgrade notes warn about and the host-side recipe that defuses
// it. A refresh token without CreationTime / Lifetime derives expires_at in
// year 1, so the first RemoveExpired deletes it although the token is still
// valid. Following the recipe (GetRefreshToken, set CreationTime / Lifetime,
// UpdateRefreshToken with the read Version) recomputes expires_at, and the
// backfilled token survives the same RemoveExpired call that removes the
// untouched legacy token.
func TestE2ELegacyRefreshTokenBackfillSurvivesRemoveExpired(t *testing.T) {
	pool := newScratchPool(t)
	ctx := t.Context()
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	tokens := store.NewRefreshTokenStore(pool, discardLogger())

	// Two legacy tokens shaped like a v0.1.x core writes them (zero
	// CreationTime and Lifetime) and one carrying a real lifetime.
	for _, h := range []string{"legacy-backfilled", "legacy-untouched"} {
		if err := tokens.StoreRefreshToken(ctx, h, &models.RefreshToken{ClientId: "client-a"}); err != nil {
			t.Fatalf("StoreRefreshToken %s: %v", h, err)
		}
	}
	if err := tokens.StoreRefreshToken(ctx, "with-lifetime", &models.RefreshToken{
		ClientId: "client-a", CreationTime: fixedNow, Lifetime: 30 * 24 * 3600}); err != nil {
		t.Fatalf("StoreRefreshToken with-lifetime: %v", err)
	}

	// The filter selects exactly the two legacy handles.
	if got, want := legacyHandles(t, pool), []string{"legacy-backfilled", "legacy-untouched"}; !slices.Equal(got, want) {
		t.Fatalf("legacy filter selected %v, want %v", got, want)
	}

	// Backfill one of them through the public port, as the recipe does.
	tok, err := tokens.GetRefreshToken(ctx, "legacy-backfilled")
	if err != nil {
		t.Fatalf("GetRefreshToken: %v", err)
	}
	tok.CreationTime = fixedNow
	tok.Lifetime = 30 * 24 * 3600
	readVersion := tok.Version
	if err := tokens.UpdateRefreshToken(ctx, "legacy-backfilled", tok, readVersion); err != nil {
		t.Fatalf("UpdateRefreshToken: %v", err)
	}
	// A backfill racing a concurrent writer still holds the old Version and
	// must be refused, so the host re-reads and retries instead of
	// overwriting the newer record.
	if err := tokens.UpdateRefreshToken(ctx, "legacy-backfilled", tok, readVersion); !errors.Is(err, storage.ErrVersionConflict) {
		t.Errorf("UpdateRefreshToken with a stale Version: err = %v, want storage.ErrVersionConflict", err)
	}

	var expiresAt time.Time
	if err := pool.QueryRow(ctx,
		"SELECT expires_at FROM refresh_tokens WHERE handle = $1", "legacy-backfilled").Scan(&expiresAt); err != nil {
		t.Fatalf("read expires_at: %v", err)
	}
	if want := fixedNow.Add(30 * 24 * time.Hour); !expiresAt.Equal(want) {
		t.Errorf("backfilled expires_at = %v, want %v", expiresAt, want)
	}
	if got, want := legacyHandles(t, pool), []string{"legacy-untouched"}; !slices.Equal(got, want) {
		t.Errorf("legacy filter after backfill selected %v, want %v", got, want)
	}

	n, err := tokens.RemoveExpired(ctx, fixedNow)
	if err != nil {
		t.Fatalf("RemoveExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("RemoveExpired removed %d, want 1 (only the untouched legacy token)", n)
	}
	assertHandles(t, pool, "refresh_tokens", true, "legacy-backfilled", "with-lifetime")
	assertHandles(t, pool, "refresh_tokens", false, "legacy-untouched")
}

// legacyHandles runs the documented legacy filter and returns the handles
// in order.
func legacyHandles(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), legacyRefreshTokenFilter)
	if err != nil {
		t.Fatalf("legacy filter: %v", err)
	}
	handles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("legacy filter rows: %v", err)
	}
	return handles
}
