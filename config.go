package main

// Process configuration: logging and the Postgres pool.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func configLogging() *slog.Logger {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	return logger
}

func configPostgres(ctx context.Context) (*pgxpool.Pool, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		// ParseConfig("") succeeds and silently falls back to libpq defaults
		// (localhost, current OS user), so reject it explicitly.
		return nil, errors.New("DATABASE_URL is not set")
	}

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}
	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	// NewWithConfig is lazy, so nothing has dialed yet. Ping proves the database
	// is reachable at boot instead of on the first request.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close() // we own the pool until we hand it back
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
