// Command migrate applies the embedded migrations and exits. Kubernetes runs
// it as a Job before the deployment rolls out.
package main

import (
	"log/slog"
	"os"

	"lpm-server/src"

	"github.com/joho/godotenv"
)

func main() {
	logger := src.ConfigLogging()

	if err := run(logger); err != nil {
		logger.Error("migrate", slog.Any("error", err))
		os.Exit(1)
	}
}

// Holds the work so main can os.Exit.
func run(logger *slog.Logger) error {
	// A missing .env is normal outside local dev.
	_ = godotenv.Load()

	if err := src.RunMigrations(os.Getenv("DATABASE_URL")); err != nil {
		return err
	}

	logger.Info("migrations applied")
	return nil
}
