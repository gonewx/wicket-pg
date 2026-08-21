# wicket-pg

PostgreSQL storage adapter for [wicket](https://github.com/gonewx/wicket).

## Status

The adapter implementation is complete: all nine store types, the migrations entry point, and the conformance contract suites are in place and passing. v0.1.0 through v0.1.3 are released (use v0.1.3; v0.1.0 is unusable through the default Go module proxy due to a distribution split).

## Prerequisites

- wicket v0.1.x (this module requires wicket v0.1.4; v0.1.3 made ClaimsIdentity JSON round trips lossless for stored subjects, and v0.1.4 adds no production code changes on top of it)
- PostgreSQL 15+
- Go 1.27+ (from v0.1.3 onward the go directive names the release, `go 1.27`; v0.1.0 through v0.1.2 permanently carry the prerelease directive `go 1.27rc1` because published tags are immutable — a released 1.27.x toolchain satisfies both, since `1.27rc1` sorts before `1.27.0`)

## Quick Start

### Install

```bash
GOWORK=off go get github.com/gonewx/wicket-pg@v0.1.3
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

`migrations.Down(ctx, pool)` is available for rollbacks.

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

## License

MIT License. See LICENSE file for details.

## Provenance

This is an independent original work. See PROVENANCE.md for the full lineage statement.
