# wicket-pg

PostgreSQL storage adapter for [wicket](https://github.com/gonewx/wicket).

## Status

The adapter implementation is complete: all nine store types, the migrations entry point (four migrations, `000001` through `000004`), and the wicket 3.0.0 conformance contract suites are in place and passing; every store's `ConformsTo()` returns `3.0.0`. v0.2.0 targets wicket v0.8.0 and is the version this document describes: for new installs, use v0.2.0. v0.1.0 through v0.1.3 are the existing wicket v0.1.x releases (v0.1.0 is unusable through the default Go module proxy due to a distribution split).

## Prerequisites

- wicket v0.8.0 (this module requires wicket v0.8.0, whose `storagetest`, `sessiontest` and `keymgmttest` suites report `SuiteVersion` 3.0.0; hosts that remain on wicket v0.1.x keep using v0.1.3, the last release built for that line — see Upgrading from v0.1.x)
- PostgreSQL 15+
- Go 1.27+ (from v0.1.3 onward the go directive names the release, `go 1.27`; v0.1.0 through v0.1.2 permanently carry the prerelease directive `go 1.27rc1` because published tags are immutable — a released 1.27.x toolchain satisfies both, since `1.27rc1` sorts before `1.27.0`)

## Quick Start

### Install

```bash
GOWORK=off go get github.com/gonewx/wicket-pg@v0.2.0
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

Nine store constructors are provided, one per wicket storage family. Each takes the host-owned `*pgxpool.Pool` and a `*slog.Logger` (a nil logger falls back to `slog.Default()`):

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
_ = store.NewSessionStore(pool, logger)
_ = store.NewKeyRecordStore(pool, logger)
```

## Upgrading from v0.1.x

Moving the host from wicket v0.1.x to v0.8.0 brings its own changes; read wicket's [CHANGELOG](https://github.com/gonewx/wicket/blob/main/CHANGELOG.md) before upgrading. v0.2.0 adds migration `000004`, which gives the authorization code, refresh token and reference token tables visible `client_id` / `session_id` columns and the device code table a visible `session_id` column, indexes them (plus `user_consents.client_id`), and backfills existing rows from their payload. The removal-by-client and removal-by-session methods rely on these columns.

1. Stop every writer still running a v0.1.x build. A v0.1.x build writing after `000004` leaves the new columns `NULL` on insert and stale on update; the backfill runs only once, inside the migration, so those rows would be silently missed by later removals by client or session.
2. Run the NUL pre-check from `docs/deployment-guide.md` section 6.2 on the authorization code, refresh token, reference token and device code tables, and clear any hits: a payload holding `\u0000` makes `000004` fail and roll back.
3. Upgrade the module to v0.2.0 (which requires wicket v0.8.0), then call `migrations.Up(ctx, pool)` to apply migration 000004 before any v0.2.0 store serves traffic. On large tables, run it in a maintenance window: it rewrites four tables in one transaction.
4. Start the v0.2.0 writers.

If your host must keep wicket v0.1.x, stay on v0.1.3: v0.2.0 requires wicket v0.8.0, and adding it raises your host's wicket requirement to that version. Do not use `migrations.Down` to back out `000004` — it reverts every migration and deletes all data. See `docs/deployment-guide.md` for the pre-upgrade check, the rollback procedure and the operational notes.

## License

MIT License. See LICENSE file for details.

## Provenance

This is an independent original work. See PROVENANCE.md for the full lineage statement.
