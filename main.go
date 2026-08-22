package main

// Process lifecycle: environment, dependency wiring, serve, graceful shutdown.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	logger := configLogging()

	// A missing .env is normal outside local dev, where the environment is
	// already populated. Anything else (bad syntax, unreadable file) is fatal.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.Error("load .env", slog.Any("error", err))
		os.Exit(1)
	}

	if err := run(logger); err != nil {
		logger.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

// run holds everything that needs deferred cleanup, so main can os.Exit safely.
func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbpool, err := configPostgres(ctx)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer dbpool.Close()

	poolConfig := dbpool.Config()
	logger.Info("database pool ready",
		slog.String("host", poolConfig.ConnConfig.Host),
		slog.String("database", poolConfig.ConnConfig.Database),
		slog.Int("maxConns", int(poolConfig.MaxConns)))

	srvApp := NewServer(dbpool)

	httpServer := &http.Server{
		Addr: ":8080",
		// RequestLogger is outermost so it records the 500 Recoverer writes.
		Handler:      Chain(srvApp.Routes(), RequestLogger(logger), Recoverer(), Timeout(5*time.Second)),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", slog.String("addr", httpServer.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	select {
	case err := <-srvErr:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down server gracefully")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown forced: %w", err)
	}

	return nil
}
