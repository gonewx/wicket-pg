# Changelog

All notable changes to this project are documented in this file; releases before 0.2.0 are summarized briefly. Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `PushedAuthorizationRequestStore`, a PostgreSQL implementation of wicket's optional `storage.PushedAuthorizationRequestStore` port (PAR, RFC 9126), built with `store.NewPushedAuthorizationRequestStore(pool, logger)`. Writes are insert-only (an existing key fails with `storage.ErrDuplicateHandle`); `GetPushedAuthorizationRequest` reads without consuming; `TakePushedAuthorizationRequest` reads and deletes in a single statement, so of several concurrent takes of one key exactly one succeeds; `RemoveExpired` deletes records whose `RetentionExpiration` precedes the cutoff and returns the count. Neither read judges expiry. Once a take's delete has committed, a decode or transport error (any error other than `storage.ErrNotFound`) means the request may already be consumed. The store passes the wicket v0.8.0 `RunPushedAuthorizationRequestStoreSuite` (`SuiteVersion` 3.0.0) on PostgreSQL 15, and its `ConformsTo()` returns `3.0.0`.
- Migration `000005_pushed_authorization_requests`: the `pushed_authorization_requests` table and one index on `retention_expiration`. It creates a new table only, touches no existing table and needs no backfill; call `migrations.Up(ctx, pool)` to apply it. The visible `client_id` and time columns are for operator queries; the store reads every record from its payload.

A PostgreSQL client registration store (`storage.ClientRegistrationStore`) is still not provided.

## [0.2.1] - 2026-11-21

Documentation, migration comment and test corrections to 0.2.0. Schema, migration SQL statements and the Go API are unchanged; only comments in `000004_scoped_removal_columns.up.sql` were updated, and no new migration is needed. Hosts that upgraded from v0.1.x, including those already running 0.2.0, must still backfill their refresh tokens as described below if they have not yet called `RefreshTokenStore.RemoveExpired`.

### Fixed

- **Do not call `RefreshTokenStore.RemoveExpired` before backfilling legacy refresh tokens.** A wicket v0.1.x core never sets `CreationTime` or `Lifetime` on a refresh token, so this adapter derives `expires_at` as `0001-01-01` for every such token and the first `RemoveExpired` deletes all of them, including tokens that are still valid. The 0.2.0 upgrade notes recommended reclaiming expired records with each store's `RemoveExpired` before applying `000004`; that recommendation is withdrawn for refresh tokens, here, in section 6 of the deployment guide and in the comments of `000004_scoped_removal_columns.up.sql`. `RemoveExpired` on the authorization code, reference token and device code stores is unaffected and may still be used before the upgrade: wicket v0.1.x sets `CreationTime` and `Lifetime` on those records. Hosts that already followed the 0.2.0 advice and called `RefreshTokenStore.RemoveExpired` cannot recover the deleted tokens from the database: restore them from a backup, or let their holders authorize again. Hosts that stay on wicket v0.1.x with wicket-pg v0.1.3 must not call `RefreshTokenStore.RemoveExpired` either: it deletes every refresh token for the same reason.
- **Backfill legacy refresh tokens before their first reclamation.** Select them with `SELECT handle FROM refresh_tokens WHERE expires_at < '0002-01-01'` (a zero `CreationTime` plus a zero `Lifetime`), then for each handle call `GetRefreshToken`, set `CreationTime` and `Lifetime`, and call `UpdateRefreshToken` with the `Version` you read; the update recomputes `expires_at`. On `storage.ErrVersionConflict`, read the token again and retry. Before starting the v0.2.x writers, disable every automatic call the host makes to `RefreshTokenStore.RemoveExpired` (startup sequence, cron job, timer), and re-enable it only after the selection query returns no rows. For a client with no lifetime limit write `Lifetime = 2147483647` (the maximum), not 0: a zero `Lifetime` sets `expires_at` to the backfill time, so the next reclamation deletes the token and the selection query no longer finds it. Take the `Lifetime` value from the table in "既有 refresh token 记录的迁移" (migrating existing refresh token records) in wicket's storage persistence guide: https://github.com/gonewx/wicket/blob/v0.8.0/docs/storage-persistence-guide.md. The adapter provides no backfill helper because it cannot see each client's lifetime settings. The full recipe is in section 6.4 of the deployment guide (in Chinese): https://github.com/gonewx/wicket-pg/blob/v0.2.1/docs/deployment-guide.md
- **Refresh tokens written before the upgrade cannot be removed by session.** The 0.2.0 entry says records written before the upgrade can be removed by client or session. wicket v0.1.x never sets a refresh token's `SessionId`, so after the `000004` backfill those tokens have no `session_id` and `RemoveRefreshTokensBySession` does not select them; removal by client works. For the other tables the backfill takes the client and session that each payload carries.

The 0.2.0 entry in CHANGELOG.md is kept as published; the items above correct its maintenance window note and the last sentence of its `000004` item.

## [0.2.0] - 2026-11-21

This release targets wicket v0.8.0. Hosts that remain on wicket v0.1.x stay on wicket-pg v0.1.3.

### Upgrade notes

- **Stop old writers, run the NUL pre-check, then apply migration `000004` with `Up` before serving traffic.** Stop every writer still running a v0.1.x build, run the NUL pre-check, then call `migrations.Up(ctx, pool)` to apply `000004_scoped_removal_columns` before any v0.2.0 store serves traffic. A v0.1.x build writing after `000004` leaves the new columns `NULL` on insert and stale on update, and the backfill runs only once, inside the migration, so those rows would be silently missed by later removals by client or session. A payload holding an escaped NUL character (`\u0000`) makes `000004` fail and roll back as a whole. The upgrade steps, the pre-check queries and the rollback procedure are in section 6 (upgrading from v0.1.x) of the deployment guide, which is written in Chinese: https://github.com/gonewx/wicket-pg/blob/v0.2.0/docs/deployment-guide.md
- **Plan a maintenance window for large tables.** `000004` rewrites the authorization code, refresh token, reference token and device code tables and builds eight indexes in one transaction, holding locks throughout. Reclaiming expired records with each store's `RemoveExpired` first shortens it.
- **Do not use `migrations.Down` to back out `000004`.** It reverts every applied migration in reverse order, `000001` included, and drops all tables and data.
- **Rolling back only the binary has a cost.** A v0.1.3 build running on the `000004` schema writes rows whose new columns are `NULL` or stale. Upgrading to v0.2.0 again does not re-run the backfill, because `Up` sees `000004` as applied; redo `000004` by hand as described in section 6.3 of the deployment guide.
- **`ConformsTo()` returns `3.0.0`.** All nine stores report the `SuiteVersion` of the wicket v0.8.0 `storagetest`, `sessiontest` and `keymgmttest` suites, which is `3.0.0`; the value is taken from the suite constants. Compare each store's `ConformsTo()` with the matching suite constant rather than a hard-coded literal.
- **The refresh token security fixes of wicket v0.6.1 and v0.7.0 require this release.** Those fixes ship in wicket itself, and wicket-pg v0.1.x cannot be combined with any wicket release that carries them: a host using this adapter gets them only by upgrading to wicket v0.8.0 together with wicket-pg v0.2.0. Moving the host from wicket v0.1.x to v0.8.0 brings its own changes; read the wicket CHANGELOG before upgrading: https://github.com/gonewx/wicket/blob/v0.8.0/CHANGELOG.md
- **No pushed authorization request or client registration store yet.** wicket v0.8.0 defines the optional `storage.PushedAuthorizationRequestStore` and `storage.ClientRegistrationStore` ports; this release does not provide PostgreSQL implementations of them.

### Added

- `AuthorizationCodeStore.TakeAuthorizationCode`: reads and deletes an authorization code in a single statement. It does not check expiry, returns `storage.ErrNotFound` when the handle is absent, and of several concurrent takes of one code exactly one succeeds. Once the delete has committed, a decode or transport error (any error other than `storage.ErrNotFound`) means the code may already be consumed; a retry then fails with `storage.ErrNotFound`.
- Removal by client and by session, each returning the number of deleted records: `RemoveAuthorizationCodesByClient` / `RemoveAuthorizationCodesBySession`, `RemoveRefreshTokensByClient` / `RemoveRefreshTokensBySession`, `RemoveReferenceTokensByClient` / `RemoveReferenceTokensBySession`, `RemoveUserConsentsByClient` and `RemoveDeviceCodesBySession`. An empty client or session id returns `(0, nil)` without touching the database.
- Migration `000004_scoped_removal_columns`: visible `client_id` and `session_id` columns on the authorization code, refresh token and reference token tables and a visible `session_id` column on the device code table, one index on each of these seven columns and one on `user_consents.client_id`, and a backfill of existing rows from their payload, all in one transaction. Records written before the upgrade can therefore be removed by client or session as well.

### Changed

- Requires wicket v0.8.0 (v0.1.3 requires wicket v0.1.4). The go directive stays `go 1.27`.
- All nine stores pass the wicket v0.8.0 contract suites (`SuiteVersion` 3.0.0) on PostgreSQL 15.

## 0.1.x

Releases for the wicket v0.1.x line. Published tags are immutable, so these notes describe them as they are.

- **[0.1.3] - 2026-08-21**: the release to stay on when combined with wicket v0.1.x, and the first one whose go directive is `go 1.27` without a prerelease suffix.
- **0.1.1 and 0.1.2 - 2026-08-08**: usable, but they carry the prerelease go directive `go 1.27rc1`.
- **0.1.0 - 2026-08-08**: do not use. Fetched through the default Go module proxy it does not build, because the content served by the proxy and checksum database differs from the repository tag.

[0.2.1]: https://github.com/gonewx/wicket-pg/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/gonewx/wicket-pg/compare/v0.1.3...v0.2.0
[0.1.3]: https://github.com/gonewx/wicket-pg/releases/tag/v0.1.3
