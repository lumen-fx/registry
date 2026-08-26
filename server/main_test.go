package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestRunReportsAPostgresFailure(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err == nil {
		t.Fatal("run returned nil, want a postgres error")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("error = %v, want it to name postgres", err)
	}
}

func TestRunMigratesOnBoot(t *testing.T) {
	t.Setenv("MIGRATE_ON_BOOT", "true")
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/nothing")

	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err == nil {
		t.Fatal("run returned nil, want the migration error")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("error = %v, want it to name migrate", err)
	}
}
