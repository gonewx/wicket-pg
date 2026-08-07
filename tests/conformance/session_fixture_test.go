// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

// Session suite fixture.
//
// The session store port suite runs against its own factory built from
// NewStore with the session store constructor, kept strictly separate from
// the grant-family fixtures: no schema name, pool, or constructor state is
// shared across the groups (AC-3, AD-9).
//
//	sessiontest.RunSessionStoreSuite(t, factory, opts...)
//
// The entry point is wired by story 1.11 when the session adapter lands;
// this file currently only declares the access point.
