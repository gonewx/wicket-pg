# wicket-pg

PostgreSQL storage adapter for [wicket](https://github.com/gonewx/wicket).

## Status

**Development in progress.** This adapter implements wicket's public storage contracts (grant store family, session, key management). wicket v0.1.x is released and the port contract is stable; the adapter implementation is the next piece of work.

## Prerequisites

- wicket v0.1.x
- PostgreSQL 15+
- Go 1.27+

## Quick Start

Installation and usage documentation will be completed once the adapter implementation lands. Dependency wiring:

```bash
GOWORK=off go get github.com/gonewx/wicket@v0.1.1
GOWORK=off go get github.com/jackc/pgx/v5
```

## License

MIT License. See LICENSE file for details.

## Provenance

This is an independent original work. See PROVENANCE.md for the full lineage statement.
