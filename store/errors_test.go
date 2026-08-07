// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gonewx/wicket/keymgmt"
	"github.com/gonewx/wicket/session"
	"github.com/gonewx/wicket/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapDuplicateErrUniqueViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23505"}

	for _, tc := range []struct {
		name     string
		sentinel error
	}{
		{name: "storage family", sentinel: storage.ErrDuplicateHandle},
		{name: "keymgmt family", sentinel: keymgmt.ErrDuplicateKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mapDuplicateErr(pgErr, tc.sentinel)
			if !errors.Is(got, tc.sentinel) {
				t.Errorf("mapDuplicateErr = %v, want %v", got, tc.sentinel)
			}
			if got != tc.sentinel {
				t.Errorf("mapDuplicateErr returned wrapped error %v, want bare sentinel", got)
			}
		})
	}
}

func TestMapDuplicateErrOtherSQLSTATEWrapped(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23503"}
	got := mapDuplicateErr(pgErr, storage.ErrDuplicateHandle)
	if errors.Is(got, storage.ErrDuplicateHandle) {
		t.Errorf("mapDuplicateErr mapped non-23505 error to duplicate sentinel: %v", got)
	}
	if !errors.Is(got, pgErr) {
		t.Errorf("mapDuplicateErr = %v, want the original error reachable via errors.Is", got)
	}
}

func TestMapDuplicateErrWrappedPgErrorPenetrates(t *testing.T) {
	inner := &pgconn.PgError{Code: "23505"}
	wrapped := fmt.Errorf("outer: %w", inner)

	got := mapDuplicateErr(wrapped, keymgmt.ErrDuplicateKey)
	if !errors.Is(got, keymgmt.ErrDuplicateKey) {
		t.Errorf("mapDuplicateErr = %v, want %v (errors.As must penetrate wrapping)", got, keymgmt.ErrDuplicateKey)
	}
}

func TestMapReadErrNoRows(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sentinel error
	}{
		{name: "storage family", sentinel: storage.ErrNotFound},
		{name: "session family", sentinel: session.ErrSessionNotFound},
		{name: "keymgmt family", sentinel: keymgmt.ErrRecordNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mapReadErr(pgx.ErrNoRows, tc.sentinel)
			if !errors.Is(got, tc.sentinel) {
				t.Errorf("mapReadErr = %v, want %v", got, tc.sentinel)
			}
			if got != tc.sentinel {
				t.Errorf("mapReadErr returned wrapped error %v, want bare sentinel", got)
			}
		})
	}
}

func TestMapReadErrWrappedNoRowsPenetrates(t *testing.T) {
	wrapped := fmt.Errorf("query failed: %w", pgx.ErrNoRows)
	got := mapReadErr(wrapped, storage.ErrNotFound)
	if !errors.Is(got, storage.ErrNotFound) {
		t.Errorf("mapReadErr = %v, want %v (errors.Is must penetrate wrapping)", got, storage.ErrNotFound)
	}
}

func TestMapReadErrOtherErrorWrapped(t *testing.T) {
	other := errors.New("connection refused")
	got := mapReadErr(other, storage.ErrNotFound)
	if errors.Is(got, storage.ErrNotFound) {
		t.Errorf("mapReadErr mapped ordinary error to missing sentinel: %v", got)
	}
	if !errors.Is(got, other) {
		t.Errorf("mapReadErr = %v, want the original error reachable via errors.Is", got)
	}
}
