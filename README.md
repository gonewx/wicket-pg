# wicket-pg

PostgreSQL storage adapter for [wicket](https://github.com/gonewx/wicket).

## Status

The adapter implementation is complete: all eleven store types, the migrations entry point (six migrations, `000001` through `000006`), and the wicket 4.0.0 conformance contract suites are in place and passing; every store's `ConformsTo()` returns `4.0.0`. v0.4.0 is the next release and targets wicket v0.10.0: once it is tagged, for new installs, use v0.4.0. It adds no store and no migration: it implements `RefreshTokenStore.RemoveOnlineRefreshTokensBySession`, the refresh token port method wicket v0.10.0 adds for revoking a session's online refresh tokens at logout, and reports the 4.0.0 suite version. Until v0.4.0 is tagged, v0.3.0 (released 2026-11-22, wicket v0.8.0) is the latest release. v0.3.0 added the pushed authorization request store (`PushedAuthorizationRequestStore`, for wicket's optional PAR feature) with its migration `000005`, and the client registration store (`ClientRegistrationStore`, for wicket's dynamic client registration and the features that depend on it) with its migration `000006`. v0.2.1 corrects the v0.2.0 upgrade notes (refresh tokens issued by wicket v0.1.x must be backfilled before `RemoveExpired`) and changes no code or schema; v0.2.0 is the first release for wicket v0.8.0. See [CHANGELOG.md](CHANGELOG.md) for the upgrade notes. v0.1.0 through v0.1.3 are the existing wicket v0.1.x releases (v0.1.0 is unusable through the default Go module proxy due to a distribution split).

## Prerequisites

- wicket v0.10.0 (this module requires wicket v0.10.0, whose `storagetest`, `sessiontest` and `keymgmttest` suites report `SuiteVersion` 4.0.0; hosts that remain on wicket v0.8.0 keep using v0.3.0, and hosts that remain on wicket v0.1.x keep using v0.1.3, the last release built for that line — see the upgrade sections)
- PostgreSQL 15+
- Go 1.27+ (from v0.1.3 onward the go directive names the release, `go 1.27`; v0.1.0 through v0.1.2 permanently carry the prerelease directive `go 1.27rc1` because published tags are immutable — a released 1.27.x toolchain satisfies both, since `1.27rc1` sorts before `1.27.0`)

## Quick Start

### Install

```bash
GOWORK=off go get github.com/gonewx/wicket-pg@v0.4.0
```

### Apply migrations

Create a `pgxpool.Pool` and call `migrations.Up` in your host startup sequence. Migrations are idempotent and never run automatically:

```go
import (
	"context"

	"github.com/gonewx/wicket-pg/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

pool, err := pgxpool.New(ctx, dsn)
if err != nil {
	// handle error
}

if err := migrations.Up(ctx, pool); err != nil {
	// handle error
}
```

`migrations.Down(ctx, pool)` reverts every applied migration in reverse order, `000001` included: it drops all tables and all stored data. It is a full teardown, not a way to undo a single migration such as `000004`.

### Inject the stores

Eleven store constructors are provided, one per wicket store port this adapter implements (`NewClientRegistrationStore` serves both `storage.ClientRegistrationStore` and `storage.ClientOriginLookup` on the same value). `NewPushedAuthorizationRequestStore` is needed only when the host enables wicket's PAR feature, which requires a `storage.PushedAuthorizationRequestStore` injected with `ServerConfig.SetPushedAuthorizationRequestStore` (or `SetPushedAuthorizationRequestStoreProvider`). `NewClientRegistrationStore` is needed only when the host enables dynamic client registration or a feature that depends on it, which requires a `storage.ClientRegistrationStore` that also implements `storage.ClientOriginLookup`, injected with `ServerConfig.SetClientRegistrationStore`; that store then serves every client read, so it replaces `SetClientStore`, `SetClients` and `SetClientsProvider`. Each takes the host-owned `*pgxpool.Pool` and a `*slog.Logger` (a nil logger falls back to `slog.Default()`):

```go
import (
	"log/slog"

	"github.com/gonewx/wicket-pg/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool   // host-owned
var logger *slog.Logger  // may be nil; falls back to slog.Default()

_ = store.NewAuthorizationCodeStore(pool, logger)
_ = store.NewRefreshTokenStore(pool, logger)
_ = store.NewReferenceTokenStore(pool, logger)
_ = store.NewUserConsentStore(pool, logger)
_ = store.NewPersistedGrantStore(pool, logger)
_ = store.NewDeviceFlowStore(pool, logger)
_ = store.NewBackchannelAuthenticationRequestStore(pool, logger)
_ = store.NewPushedAuthorizationRequestStore(pool, logger)
_ = store.NewClientRegistrationStore(pool, logger)
_ = store.NewSessionStore(pool, logger)
_ = store.NewKeyRecordStore(pool, logger)
```

### Seed existing clients before enabling dynamic client registration

Once `SetClientRegistrationStore` is used, the client registration store is the only client source: wicket panics at `Freeze()` if `SetClientStore`, `SetClients` or `SetClientsProvider` is also used, and it has no option for static clients. Before switching, write every existing first-party or static client into `client_registrations` with one `RegisterClient` call per client. Do it in this order: freeze changes to the old client source, seed, then switch the configuration; during a rolling deployment, old and new instances read clients from different sources.

- Pass the `Client` as it is. The client is stored with `storage.EncodeClient`, which does not hash anything: each `Secret.Value` must already hold the hashed secret, never the plaintext, in the same hashed form as in `SetClients` (wicket's `utils.Sha256`, the form wicket's registration endpoint stores).
- `RegistrationAccessTokenDigest`: leave it empty (`""`), and wicket's client management endpoints (RFC 7592) refuse every request for that client; this is the recommended choice for first-party clients. Alternatively, generate a registration access token yourself, store `clientregistration.RegistrationAccessTokenDigest(token)` as the digest and keep the token on your side.
- `RegistrationTime`: use the client's original creation time or the time of seeding, not the zero value: the management responses report it as `client_id_issued_at`.
- Running the seeding again returns `storage.ErrDuplicateHandle` for each client already present; treat it as already seeded. It only means the id exists: a rerun does not update a client that has since changed in the source. To change a seeded client later, read it with `FindClientRegistration` and write it with `UpdateClientRegistration` and the revision you read.
- Stop on any error other than `storage.ErrDuplicateHandle`, and do not switch until every client has been seeded or already exists.
- Switching back to `SetClients` or `SetClientStore` later does not carry over clients registered or changed through dynamic client registration.
- A removed client id leaves a tombstone and cannot be seeded again.

## Upgrading from v0.3.x

v0.4.0 requires wicket v0.10.0, so upgrading the module raises your host's wicket requirement to that version; read wicket's [CHANGELOG](https://github.com/gonewx/wicket/blob/v0.10.0/CHANGELOG.md) for the changes between v0.8.0 and v0.10.0 before upgrading. On the adapter side:

- Upgrade the module to v0.4.0 with `go get`. There is no new migration and no backfill; calling `migrations.Up(ctx, pool)` as usual is enough.
- Every store's `ConformsTo()` now returns `4.0.0`. A host that compares it with the suite constants it builds against needs no change; a host that compares it with a hard-coded `3.0.0` must update that value.
- `RefreshTokenStore.RemoveOnlineRefreshTokensBySession` removes the refresh tokens of a session whose scopes do not hold the exact element `offline_access` (`offline_access_extended` does not count) and keeps offline tokens. wicket calls it at logout when `ServerConfig.SetRevokeOnlineRefreshTokensOnLogout(true)` or the high security profile is in effect. It reaches only tokens stored with a session: wicket records the session on online refresh tokens while that switch is in effect, so tokens issued before it was turned on, and tokens stored without a session (`session_id` is `NULL`, for example tokens issued by wicket v0.1.x), are never selected. wicket's built-in issuance creates refresh tokens only when `offline_access` was requested; the switch concerns online tokens written by your own services, extension grants or migrated data. The scopes are judged from each stored token, so no migration is needed.

If your host must stay on wicket v0.8.0, stay on v0.3.0: it cannot be combined with wicket v0.10.0, whose refresh token port has the additional method.

## Upgrading from v0.2.x

Upgrade the module to v0.4.0 with `go get` (it requires wicket v0.10.0; see Upgrading from v0.3.x), then call `migrations.Up(ctx, pool)`. v0.3.0 adds migrations `000005` and `000006`, which each create one new table (`pushed_authorization_requests` and `client_registrations`); they touch no existing table and need no backfill. A host that enables PAR must call `PushedAuthorizationRequestStore.RemoveExpired` periodically; wicket does not reclaim these records for it. A host that enables dynamic client registration must seed its existing clients first (see above).

## Upgrading from v0.1.x

Moving the host from wicket v0.1.x to v0.10.0 brings its own changes; read wicket's [CHANGELOG](https://github.com/gonewx/wicket/blob/v0.10.0/CHANGELOG.md) before upgrading. v0.2.x adds migration `000004`, which gives the authorization code, refresh token and reference token tables visible `client_id` / `session_id` columns and the device code table a visible `session_id` column, indexes them (plus `user_consents.client_id`), and backfills existing rows from their payload. The removal-by-client and removal-by-session methods rely on these columns.

1. Stop every writer still running a v0.1.x build. A v0.1.x build writing after `000004` leaves the new columns `NULL` on insert and stale on update; the backfill runs only once, inside the migration, so those rows would be silently missed by later removals by client or session.
2. Run the NUL pre-check from `docs/deployment-guide.md` section 6.2 on the authorization code, refresh token, reference token and device code tables, and clear any hits: a payload holding `\u0000` makes `000004` fail and roll back.
3. Upgrade the module to v0.4.0 (which requires wicket v0.10.0), then call `migrations.Up(ctx, pool)` to apply migration 000004 before any v0.4.0 store serves traffic. The same `Up` also applies `000005` and `000006`, which each create one new table and need no backfill. On large tables, run it in a maintenance window: it rewrites four tables in one transaction. Expired authorization codes, reference tokens and device codes may be reclaimed with their store's `RemoveExpired` first; refresh tokens may not.
4. Before starting the v0.4.0 writers, disable every automatic call your host makes to `RefreshTokenStore.RemoveExpired` (startup sequence, cron job, timer); re-enable it only after the backfill's confirmation query in section 6.4 returns no rows. Then start the v0.4.0 writers.
5. Backfill refresh tokens issued by wicket v0.1.x before reclaiming any. Do not call `RemoveExpired` on refresh tokens before the backfill: wicket v0.1.x never sets their `CreationTime` or `Lifetime`, so their derived expiry is in year 1 and `RemoveExpired` deletes every one of them, valid or not. The recipe (select with `expires_at < '0002-01-01'`, then `GetRefreshToken`, set `CreationTime` / `Lifetime`, `UpdateRefreshToken` with the version read) is in `docs/deployment-guide.md` section 6.4. These tokens also carry no session, so neither `RemoveRefreshTokensBySession` nor `RemoveOnlineRefreshTokensBySession` removes them. If you already called `RemoveExpired` on refresh tokens, the deleted tokens can only be restored from a backup, or their holders must authorize again.

If your host must keep wicket v0.1.x, stay on v0.1.3: v0.2.x and v0.3.x require wicket v0.8.0 and v0.4.0 requires wicket v0.10.0, and adding any of them raises your host's wicket requirement to that version. On wicket v0.1.x, do not call `RefreshTokenStore.RemoveExpired`: those refresh tokens have no expiry, so it deletes all of them. Do not use `migrations.Down` to back out `000004` — it reverts every migration and deletes all data. See `docs/deployment-guide.md` for the pre-upgrade check, the refresh token backfill, the rollback procedure and the operational notes.

## License

MIT License. See LICENSE file for details.

## Provenance

This is an independent original work. See PROVENANCE.md for the full lineage statement.
