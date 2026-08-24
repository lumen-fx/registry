package src

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
	// WriteTimeout stays above it so handlers can still reply.
	requestTimeout = 5 * time.Second
	shutdownGrace  = 10 * time.Second
)

func NewHTTPServer(logger *slog.Logger, db *pgxpool.Pool, addr string) *http.Server {
	app := NewServer(db)

	return &http.Server{
		Addr: addr,
		// Outermost, so it logs the 500 Recoverer writes.
		Handler:      Chain(app.Routes(), RequestLogger(logger), Recoverer(), Timeout(requestTimeout)),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

// Serve runs until ctx is cancelled, then drains requests.
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
