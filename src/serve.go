package src

// HTTP server construction and lifecycle. Kept here rather than in main so the
// wiring is reachable from tests.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// requestTimeout bounds a single request; WriteTimeout stays above it so a
	// timed-out handler still gets to write its response.
	requestTimeout = 5 * time.Second
	shutdownGrace  = 10 * time.Second
)

// NewHTTPServer builds the server with its middleware stack and timeouts.
func NewHTTPServer(logger *slog.Logger, db *pgxpool.Pool, addr string) *http.Server {
	app := NewServer(db)

	return &http.Server{
		Addr: addr,
		// RequestLogger is outermost so it records the 500 Recoverer writes.
		Handler:      Chain(app.Routes(), RequestLogger(logger), Recoverer(), Timeout(requestTimeout)),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

// Serve runs the server until ctx is cancelled, then drains in-flight requests.
// A listen failure returns immediately instead of waiting for the signal.
func Serve(ctx context.Context, logger *slog.Logger, db *pgxpool.Pool, addr string) error {
	poolConfig := db.Config()
	logger.Info("database pool ready",
		slog.String("host", poolConfig.ConnConfig.Host),
		slog.String("database", poolConfig.ConnConfig.Database),
		slog.Int("maxConns", int(poolConfig.MaxConns)))

	httpServer := NewHTTPServer(logger, db, addr)

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown forced: %w", err)
	}

	return nil
}
