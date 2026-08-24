package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"lpm-server/src"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
)

const listenAddr = ":8080"

func main() {
	logger := src.ConfigLogging()

	// A missing .env is normal. Bad syntax is fatal.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.Error("load .env", slog.Any("error", err))
		os.Exit(1)
	}

	if err := run(logger); err != nil {
		logger.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

// Holds deferred cleanup so main can os.Exit.
func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbpool, err := src.ConfigPostgres(ctx)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer dbpool.Close()

	return src.Serve(ctx, logger, dbpool, listenAddr)
}
