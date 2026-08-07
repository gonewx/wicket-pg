// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

// Package store implements the pgx-backed adapters for the wicket port
// families. All store types live in this single package and share one
// unexported base.
//
// Constructor shape: every store type is built by a constructor of the form
//
//	NewXxxStore(pool *pgxpool.Pool, logger *slog.Logger) *XxxStore
//
// A constructor only assembles the shared base; it never creates or closes
// the pool. The pool lifecycle belongs to the host, and a nil logger falls
// back to slog.Default().
//
// Error mapping follows a single rule enforced by the shared base: SQLSTATE
// 23505 (unique violation) maps to the port family's duplicate sentinel,
// pgx.ErrNoRows maps to the port family's missing sentinel, and every other
// infrastructure error is wrapped with %w for errors.Is. Single-record reads
// never return (nil, nil).
//
// List methods return an empty, non-nil slice (e.g. []*Xxx{}) when the query
// matches nothing; they do not return a sentinel error for an empty result.
package store

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// baseStore carries the resources shared by every store type: the host-owned
// connection pool, the injected logger, and the payload codec.
type baseStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	codec  *payloadCodec
}

// newBase assembles the shared base. A nil logger falls back to
// slog.Default(); no new logger instance is constructed.
func newBase(pool *pgxpool.Pool, logger *slog.Logger) baseStore {
	if logger == nil {
		logger = slog.Default()
	}
	return baseStore{
		pool:   pool,
		logger: logger,
		codec:  &payloadCodec{},
	}
}
