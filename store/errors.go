// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// mapDuplicateErr maps a unique-violation to dupSentinel and wraps every
// other error with %w. The SQLSTATE 23505 check penetrates wrapping layers
// via errors.As.
func mapDuplicateErr(err error, dupSentinel error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return dupSentinel
	}
	return fmt.Errorf("%w", err)
}

// mapReadErr maps a no-rows result to missingSentinel and wraps every other
// error with %w. A single-record read must never surface (nil, nil).
func mapReadErr(err error, missingSentinel error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return missingSentinel
	}
	return fmt.Errorf("%w", err)
}
