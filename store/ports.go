// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"github.com/gonewx/wicket/keymgmt"
	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/storage"
)

// Compile-time port assertions: each store must satisfy the full method set
// of its wicket port, so a port that gains a method fails the build here
// rather than at a host's wiring site.
var (
	_ storage.AuthorizationCodeStore                = (*AuthorizationCodeStore)(nil)
	_ storage.RefreshTokenStore                     = (*RefreshTokenStore)(nil)
	_ storage.ReferenceTokenStore                   = (*ReferenceTokenStore)(nil)
	_ storage.UserConsentStore                      = (*UserConsentStore)(nil)
	_ storage.PersistedGrantStore                   = (*PersistedGrantStore)(nil)
	_ storage.DeviceFlowStore                       = (*DeviceFlowStore)(nil)
	_ storage.BackchannelAuthenticationRequestStore = (*BackchannelAuthenticationRequestStore)(nil)
	_ storage.PushedAuthorizationRequestStore       = (*PushedAuthorizationRequestStore)(nil)
	_ storage.ClientRegistrationStore               = (*ClientRegistrationStore)(nil)
	_ storage.ClientOriginLookup                    = (*ClientRegistrationStore)(nil)
	_ session.Store                                 = (*SessionStore)(nil)
	_ keymgmt.RecordStore                           = (*KeyRecordStore)(nil)
)
