// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

// Key-record suite fixture.
//
// The key management record store port suite runs against its own factory
// built from NewStore with the key-record store constructor, kept strictly
// separate from the grant-family and session fixtures: no schema name, pool,
// or constructor state is shared across the groups (AC-3, AD-9).
//
//	keymgmttest.RunRecordStoreSuite(t, factory, opts...)
//
// The entry point is wired by story 1.12 when the key-record adapter lands;
// this file currently only declares the access point.
