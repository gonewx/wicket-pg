// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package e2e_test pins the unified expiry reclamation semantics across all
// eight lifecycle stores against the real schema: RemoveExpired on the seven
// grant-family stores and DeleteExpired on the session store each reclaim
// only records whose stored expiry strictly precedes the caller-supplied
// cutoff — boundary records (expires_at == cutoff) survive, NULL-expiry
// records are never touched, the returned count matches the deleted rows
// exactly, a later cutoff reclaims the survivors, and expired records stay
// readable until reclaimed (no SQL-level expiry filtering). The
// no-background-goroutine property is covered by each conformance suite's
// NoBackgroundGoroutines MUST case and is not re-tested here. The suite is
// gated behind WICKET_PG_TEST_DATABASE_URL so the plain test run stays
// green without a database.
package e2e_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/gonewx/wicket-pg/store"
	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/storage"
	"github.com/gonewx/wicket/storage/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reclamationFixture bundles the four semantic categories' identifiers and
// the store-specific read/reclaim entry points bound to one store instance.
// The read closure returns nil while the record is readable and the store's
// missing sentinel after reclamation; reclaim is the RemoveExpired or
// DeleteExpired entry. The null identifier is empty for the CreationTime +
// Lifetime family, whose writes always derive a non-NULL expires_at.
type reclamationFixture struct {
	expired, boundary, alive, null string
	read                           func(ctx context.Context, key string) error
	notFound                       error
	reclaim                        func(ctx context.Context, cutoff time.Time) (int, error)
}

// TestE2EExpiryReclamationUnified is the unified eight-store reclamation
// matrix (AC-1/AC-2/AC-4/AC-5): one table-driven case per lifecycle store,
// each seeding expired / boundary / alive / NULL-expiry records and driving
// the same assertion sequence — pre-cleanup readability of expired records,
// exact reclaim count, sentinel reads after cleanup, survivor retention, and
// a second reclaim with a larger cutoff that takes boundary plus alive with
// an exact count while NULL rows survive forever.
func TestE2EExpiryReclamationUnified(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, pool *pgxpool.Pool) reclamationFixture
	}{
		{"authorization_code", seedAuthorizationCodeReclamation},
		{"refresh_token", seedRefreshTokenReclamation},
		{"reference_token", seedReferenceTokenReclamation},
		{"user_consent", seedUserConsentReclamation},
		{"persisted_grant", seedPersistedGrantReclamation},
		{"device_flow", seedDeviceFlowReclamation},
		{"backchannel", seedBackchannelReclamation},
		{"session", seedSessionReclamation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newScratchPool(t)
			if err := migrations.Up(t.Context(), pool); err != nil {
				t.Fatalf("Up: %v", err)
			}
			fx := tc.seed(t, pool)

			// An expired record remains readable until cleanup — the store
			// does no expiry visibility filtering of its own.
			if err := fx.read(t.Context(), fx.expired); err != nil {
				t.Errorf("expired record not readable before cleanup: %v", err)
			}

			n, err := fx.reclaim(t.Context(), fixedNow)
			if err != nil {
				t.Fatalf("reclaim: %v", err)
			}
			if n != 1 {
				t.Errorf("reclaimed %d rows, want 1 (expired only)", n)
			}

			if err := fx.read(t.Context(), fx.expired); !errors.Is(err, fx.notFound) {
				t.Errorf("expired record after cleanup error = %v, want %v", err, fx.notFound)
			}
			survivors := []string{fx.boundary, fx.alive}
			if fx.null != "" {
				survivors = append(survivors, fx.null)
			}
			for _, key := range survivors {
				if err := fx.read(t.Context(), key); err != nil {
					t.Errorf("record %q removed by first reclaim: %v", key, err)
				}
			}

			// A later cutoff reclaims the survivors: boundary (expires_at ==
			// cutoff) plus alive, exactly — the NULL row is never counted.
			n, err = fx.reclaim(t.Context(), fixedAlive.Add(time.Hour))
			if err != nil {
				t.Fatalf("reclaim (later cutoff): %v", err)
			}
			if n != 2 {
				t.Errorf("reclaimed %d rows, want 2 (boundary + alive)", n)
			}
			for _, key := range []string{fx.boundary, fx.alive} {
				if err := fx.read(t.Context(), key); !errors.Is(err, fx.notFound) {
					t.Errorf("record %q after later cutoff error = %v, want %v", key, err, fx.notFound)
				}
			}
			if fx.null != "" {
				if err := fx.read(t.Context(), fx.null); err != nil {
					t.Errorf("NULL-expiry record removed by later cutoff: %v", err)
				}
			}
		})
	}
}

// seedAuthorizationCodeReclamation seeds the CreationTime + Lifetime family
// shape: expires_at is derived as CreationTime + Lifetime seconds with no
// zero-value special case, so there is no NULL form.
func seedAuthorizationCodeReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewAuthorizationCodeStore(pool, discardLogger())
	codes := []struct {
		handle   string
		creation time.Time
		lifetime int
	}{
		{"reclaim-ac-expired", fixedNow.Add(-120 * time.Second), 60}, // expires fixedNow - 60s
		{"reclaim-ac-boundary", fixedNow.Add(-60 * time.Second), 60}, // expires == fixedNow
		{"reclaim-ac-alive", fixedNow, 3600},                         // expires fixedNow + 1h
	}
	for _, c := range codes {
		if err := s.StoreAuthorizationCode(t.Context(), c.handle, &models.AuthorizationCode{
			CreationTime: c.creation,
			Lifetime:     c.lifetime,
			ClientId:     "client-reclaim",
		}); err != nil {
			t.Fatalf("StoreAuthorizationCode (%s): %v", c.handle, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-ac-expired",
		boundary: "reclaim-ac-boundary",
		alive:    "reclaim-ac-alive",
		read: func(ctx context.Context, key string) error {
			_, err := s.GetAuthorizationCode(ctx, key)
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedRefreshTokenReclamation seeds the refresh token store with the same
// derived-expiry shape as the authorization code family.
func seedRefreshTokenReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewRefreshTokenStore(pool, discardLogger())
	tokens := []struct {
		handle   string
		creation time.Time
		lifetime int
	}{
		{"reclaim-rt-expired", fixedNow.Add(-120 * time.Second), 60},
		{"reclaim-rt-boundary", fixedNow.Add(-60 * time.Second), 60},
		{"reclaim-rt-alive", fixedNow, 3600},
	}
	for _, c := range tokens {
		if err := s.StoreRefreshToken(t.Context(), c.handle, &models.RefreshToken{
			CreationTime: c.creation,
			Lifetime:     c.lifetime,
			ClientId:     "client-reclaim",
		}); err != nil {
			t.Fatalf("StoreRefreshToken (%s): %v", c.handle, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-rt-expired",
		boundary: "reclaim-rt-boundary",
		alive:    "reclaim-rt-alive",
		read: func(ctx context.Context, key string) error {
			_, err := s.GetRefreshToken(ctx, key)
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedReferenceTokenReclamation seeds the reference token store with the
// same derived-expiry shape as the authorization code family.
func seedReferenceTokenReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewReferenceTokenStore(pool, discardLogger())
	tokens := []struct {
		handle   string
		creation time.Time
		lifetime int
	}{
		{"reclaim-ref-expired", fixedNow.Add(-120 * time.Second), 60},
		{"reclaim-ref-boundary", fixedNow.Add(-60 * time.Second), 60},
		{"reclaim-ref-alive", fixedNow, 3600},
	}
	for _, c := range tokens {
		if err := s.StoreReferenceToken(t.Context(), c.handle, &models.Token{
			CreationTime: c.creation,
			Lifetime:     c.lifetime,
			ClientId:     "client-reclaim",
		}); err != nil {
			t.Fatalf("StoreReferenceToken (%s): %v", c.handle, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-ref-expired",
		boundary: "reclaim-ref-boundary",
		alive:    "reclaim-ref-alive",
		read: func(ctx context.Context, key string) error {
			_, err := s.GetReferenceToken(ctx, key)
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedUserConsentReclamation seeds the Expiration-pointer family shape: a
// nil pointer stores NULL and is never cleaned; a pointer equal to the
// cutoff survives the first reclaim via strict less-than.
func seedUserConsentReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewUserConsentStore(pool, discardLogger())
	expiredAt := fixedExpired
	boundaryAt := fixedNow
	aliveAt := fixedAlive
	consents := []struct {
		subject string
		expiry  *time.Time
	}{
		{"reclaim-consent-expired", &expiredAt},
		{"reclaim-consent-boundary", &boundaryAt}, // expires_at == cutoff, must survive
		{"reclaim-consent-alive", &aliveAt},
		{"reclaim-consent-null", nil}, // never expiring, must survive
	}
	for _, c := range consents {
		if err := s.StoreUserConsent(t.Context(), &models.Consent{
			SubjectId:    c.subject,
			ClientId:     "client-reclaim",
			CreationTime: fixedNow,
			Scopes:       []string{"openid"},
			Expiration:   c.expiry,
		}); err != nil {
			t.Fatalf("StoreUserConsent (%s): %v", c.subject, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-consent-expired",
		boundary: "reclaim-consent-boundary",
		alive:    "reclaim-consent-alive",
		null:     "reclaim-consent-null",
		read: func(ctx context.Context, key string) error {
			_, err := s.GetUserConsent(ctx, key, "client-reclaim")
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedPersistedGrantReclamation seeds the persisted grant store with the
// same Expiration-pointer shape as the consent store.
func seedPersistedGrantReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewPersistedGrantStore(pool, discardLogger())
	expiredAt := fixedExpired
	boundaryAt := fixedNow
	aliveAt := fixedAlive
	grants := []struct {
		key    string
		expiry *time.Time
	}{
		{"reclaim-grant-expired", &expiredAt},
		{"reclaim-grant-boundary", &boundaryAt}, // expires_at == cutoff, must survive
		{"reclaim-grant-alive", &aliveAt},
		{"reclaim-grant-null", nil}, // never expiring, must survive
	}
	for _, g := range grants {
		if err := s.Store(t.Context(), &models.PersistedGrant{
			Key:          g.key,
			Type:         "refresh_token",
			SubjectId:    "subject-reclaim",
			SessionId:    "session-reclaim",
			ClientId:     "client-reclaim",
			CreationTime: fixedNow,
			Expiration:   g.expiry,
		}); err != nil {
			t.Fatalf("Store (%s): %v", g.key, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-grant-expired",
		boundary: "reclaim-grant-boundary",
		alive:    "reclaim-grant-alive",
		null:     "reclaim-grant-null",
		read: func(ctx context.Context, key string) error {
			_, err := s.Get(ctx, key)
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedDeviceFlowReclamation seeds the device flow store with the derived
// CreationTime + Lifetime shape (no NULL form).
func seedDeviceFlowReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewDeviceFlowStore(pool, discardLogger())
	codes := []struct {
		code     string
		creation time.Time
		lifetime int
	}{
		{"reclaim-df-expired", fixedNow.Add(-120 * time.Second), 60},
		{"reclaim-df-boundary", fixedNow.Add(-60 * time.Second), 60},
		{"reclaim-df-alive", fixedNow, 3600},
	}
	for _, c := range codes {
		if err := s.StoreDeviceAuthorization(t.Context(), c.code, "user-"+c.code, &models.DeviceCode{
			CreationTime: c.creation,
			Lifetime:     c.lifetime,
			ClientId:     "client-reclaim",
		}); err != nil {
			t.Fatalf("StoreDeviceAuthorization (%s): %v", c.code, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-df-expired",
		boundary: "reclaim-df-boundary",
		alive:    "reclaim-df-alive",
		read: func(ctx context.Context, key string) error {
			_, err := s.FindByDeviceCode(ctx, key)
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedBackchannelReclamation seeds the ExpirationTime-family shape: a zero
// ExpirationTime stores NULL and is never cleaned.
func seedBackchannelReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewBackchannelAuthenticationRequestStore(pool, discardLogger())
	requests := []struct {
		id     string
		expiry time.Time
	}{
		{"reclaim-bc-expired", fixedExpired},
		{"reclaim-bc-boundary", fixedNow}, // expires_at == cutoff, must survive
		{"reclaim-bc-alive", fixedAlive},
		{"reclaim-bc-null", time.Time{}}, // zero value -> NULL, never cleaned
	}
	for _, r := range requests {
		if err := s.StoreBackchannelAuthenticationRequest(t.Context(), r.id, &models.BackchannelAuthenticationRequest{
			RequestId:      r.id,
			ClientId:       "client-reclaim",
			ExpirationTime: r.expiry,
		}); err != nil {
			t.Fatalf("StoreBackchannelAuthenticationRequest (%s): %v", r.id, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-bc-expired",
		boundary: "reclaim-bc-boundary",
		alive:    "reclaim-bc-alive",
		null:     "reclaim-bc-null",
		read: func(ctx context.Context, key string) error {
			_, err := s.FindBackchannelAuthenticationRequest(ctx, key)
			return err
		},
		notFound: storage.ErrNotFound,
		reclaim:  s.RemoveExpired,
	}
}

// seedSessionReclamation seeds the session store's expires-column shape: a
// zero Expires stores NULL and is never cleaned; reads surface
// session.ErrSessionNotFound.
func seedSessionReclamation(t *testing.T, pool *pgxpool.Pool) reclamationFixture {
	t.Helper()
	s := store.NewSessionStore(pool, discardLogger())
	sessions := []struct {
		id      string
		expires time.Time
	}{
		{"reclaim-sess-expired", fixedExpired},
		{"reclaim-sess-boundary", fixedNow}, // expires == cutoff, must survive
		{"reclaim-sess-alive", fixedAlive},
		{"reclaim-sess-null", time.Time{}}, // zero value -> NULL, never cleaned
	}
	for _, r := range sessions {
		if err := s.CreateSession(t.Context(), &session.Record{
			SessionID: r.id,
			Expires:   r.expires,
		}); err != nil {
			t.Fatalf("CreateSession (%s): %v", r.id, err)
		}
	}
	return reclamationFixture{
		expired:  "reclaim-sess-expired",
		boundary: "reclaim-sess-boundary",
		alive:    "reclaim-sess-alive",
		null:     "reclaim-sess-null",
		read: func(ctx context.Context, key string) error {
			_, err := s.GetSession(ctx, key)
			return err
		},
		notFound: session.ErrSessionNotFound,
		reclaim:  s.DeleteExpired,
	}
}
