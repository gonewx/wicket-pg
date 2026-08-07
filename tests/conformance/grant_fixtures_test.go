// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package conformance

// Grant-family suite fixtures.
//
// The seven grant storage port suites each run against their own factory
// built from NewStore with the matching store constructor, so every suite
// case gets a brand-new empty store in its own schema:
//
//	storagetest.RunAuthorizationCodeStoreSuite(t, factory, opts...)
//	storagetest.RunRefreshTokenStoreSuite(t, factory, opts...)
//	storagetest.RunReferenceTokenStoreSuite(t, factory, opts...)
//	storagetest.RunUserConsentStoreSuite(t, factory, opts...)
//	storagetest.RunPersistedGrantStoreSuite(t, factory, opts...)
//	storagetest.RunDeviceFlowStoreSuite(t, factory, opts...)
//	storagetest.RunBackchannelAuthenticationRequestStoreSuite(t, factory, opts...)
//
// Each factory is independent: no schema name, pool, or constructor state is
// shared between the seven entries (AC-3, AD-9). The entry points are wired
// by stories 1.4-1.10 as each store adapter lands; this file currently only
// declares the access points, not the constructors.
