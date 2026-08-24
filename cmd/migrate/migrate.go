package main

import (
	"log/slog"
	"os"

	"lpm-server/src"

	"github.com/joho/godotenv"
)

// Holds the work so main can os.Exit.
func run() error {
	logger := src.ConfigLogging()

	// A missing .env is normal outside local dev.
	_ = godotenv.Load()

	if err := src.RunMigrations(os.Getenv("DATABASE_URL")); err != nil {
		logger.Error("migrate", slog.Any("error", err))
		return err
	}

	logger.Info("migrations applied")
	return nil
}
