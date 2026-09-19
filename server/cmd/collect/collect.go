package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lumen-fx/registry/server/src"

	"github.com/joho/godotenv"
)

// Holds the work so main can os.Exit.
func run() error {
	logger := src.ConfigLogging()

	// A missing .env is normal outside local dev.
	_ = godotenv.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := src.ConfigPostgres(ctx)
	if err != nil {
		logger.Error("postgres", slog.Any("error", err))
		return err
	}
	defer db.Close()

	if err := src.Collect(ctx, logger, db); err != nil {
		logger.Error("collect", slog.Any("error", err))
		return err
	}

	return nil
}
