# Provenance Statement

This repository (`wicket-pg`) is an **independent original work** by Decker.

## Original Creation

All code, database schema designs, migrations, tests, and documentation in this repository are **self-authored** and created independently for this project. This adapter does not derive from, copy, or adapt any upstream source code.

## Relationship to wicket

This adapter implements the public storage interfaces defined by the `wicket` project (github.com/gonewx/wicket). It depends on wicket's **published API contracts only** — specifically the storage port interfaces, session interfaces, and key management interfaces defined in wicket's public packages.

**This adapter does NOT:**
- Copy or adapt any implementation code from wicket
- Replicate wicket's internal model structures or private types
- Contain any code that originated from wicket's dependencies or their upstream sources

## Database Schema Design

The PostgreSQL schema in `migrations/` is an **independent design** derived from:
- The read/write access patterns of wicket's public storage interfaces
- PostgreSQL best practices and performance considerations
- Standard relational database normalization principles

The schema design does NOT replicate or mirror any internal data structures from wicket or its dependencies.

## Testing and Conformance

This adapter includes conformance tests that verify compliance with wicket's public contract test suites. These tests exercise the adapter's implementation against wicket's published behavioral specifications, but do not copy test logic or assertions from wicket's internal test suite.

## License

This work is licensed under the MIT License. See LICENSE file for details.

---

**Statement Date:** 2026-07-31  
**Author:** Decker
