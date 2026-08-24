package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunReportsAMigrationFailure(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/nothing")

	if err := run(discardLogger()); err == nil {
		t.Error("run returned nil against an unreachable database")
	}
}

func TestRunAppliesMigrations(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	t.Setenv("DATABASE_URL", dsn)

	// Already migrated by the time this runs, or migrating now. Either way it
	// reports success.
	if err := run(discardLogger()); err != nil {
		t.Errorf("run = %v, want nil", err)
	}
}

func TestRunNeedsADatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	err := run(discardLogger())
	if err == nil {
		t.Fatal("run returned nil with no DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "database url") {
		t.Errorf("error = %v, want it to name the missing url", err)
	}
}
