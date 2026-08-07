// Copyright 2026 Decker.
// This file is part of wicket-pg, an independent original work.

package store

import (
	"io"
	"log/slog"
	"testing"
)

func TestNewBaseNilLoggerFallsBackToDefault(t *testing.T) {
	b := newBase(nil, nil)
	if b.logger != slog.Default() {
		t.Errorf("newBase(nil, nil).logger = %v, want slog.Default()", b.logger)
	}
	if b.pool != nil {
		t.Errorf("newBase(nil, nil).pool = %v, want nil", b.pool)
	}
	if b.codec == nil {
		t.Error("newBase(nil, nil).codec is nil, want a codec reference")
	}
}

func TestNewBasePreservesInjectedLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := newBase(nil, logger)
	if b.logger != logger {
		t.Errorf("newBase(nil, logger).logger = %v, want the injected logger", b.logger)
	}
}
