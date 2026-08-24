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
