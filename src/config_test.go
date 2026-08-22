package src

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestConfigLogging(t *testing.T) {
	// slog.SetDefault is process-global; put the old default back.
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	logger := ConfigLogging()
	if logger == nil {
		t.Fatal("ConfigLogging returned nil")
	}
	if slog.Default() != logger {
		t.Error("ConfigLogging did not install its logger as the default")
	}
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug is enabled, want info as the floor")
	}
	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info is disabled, want it enabled")
	}
}

func TestConfigPostgresRejectsUnsetURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	pool, err := ConfigPostgres(context.Background())
	if err == nil {
		pool.Close()
		t.Fatal("ConfigPostgres succeeded with no DATABASE_URL, want an error")
	}
	// An empty string parses fine and silently falls back to libpq defaults,
	// so this must be rejected by name rather than by parse failure.
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error = %v, want it to name DATABASE_URL", err)
	}
}

func TestConfigPostgresRejectsUnparsableURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user@host:not-a-port/db")

	pool, err := ConfigPostgres(context.Background())
	if err == nil {
		pool.Close()
		t.Fatal("ConfigPostgres succeeded on an unparsable URL, want an error")
	}
	if !strings.Contains(err.Error(), "parse connection string") {
		t.Errorf("error = %v, want a parse failure", err)
	}
}

func TestConfigPostgresRejectsUnreachableDatabase(t *testing.T) {
	// Port 1 refuses immediately, so the ping fails well inside its timeout.
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/testdb")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := ConfigPostgres(ctx)
	if err == nil {
		pool.Close()
		t.Fatal("ConfigPostgres succeeded against a dead port, want an error")
	}
	if !strings.Contains(err.Error(), "ping database") {
		t.Errorf("error = %v, want a ping failure", err)
	}
}
