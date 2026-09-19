package main

import (
	"os"
	"strings"
	"testing"

	"github.com/lumen-fx/registry/server/src"
)

func TestRunNeedsADatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	err := run()
	if err == nil {
		t.Fatal("run returned nil with no DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error = %v, want it to name the missing url", err)
	}
}

func TestRunReportsAnUnreachableDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/nothing")

	if err := run(); err == nil {
		t.Error("run returned nil against an unreachable database")
	}
}

// A pass that reaches nothing on GitHub still succeeds: a sample that fails
// is a warning and the next day's run tries again, so a CronJob does not go
// red over an outage.
func TestRunSurvivesAnUnreachableGitHub(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("GITHUB_API_URL", "http://127.0.0.1:1")

	// This package does not share the server suite's schema setup, and the
	// tests of the two run against the same database in any order.
	if err := src.RunMigrations(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := run(); err != nil {
		t.Errorf("run = %v, want nil", err)
	}
}
