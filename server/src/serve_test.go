package src

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lazyPool never dials. The first connection is deferred.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	config, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/testdb")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	config.MaxConns = 3

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

func TestNewHTTPServer(t *testing.T) {
	srv := NewHTTPServer(discardLogger(), lazyPool(t), "127.0.0.1:0")

	if srv.Addr != "127.0.0.1:0" {
		t.Errorf("Addr = %q, want 127.0.0.1:0", srv.Addr)
	}
	if srv.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want 10s", srv.ReadTimeout)
	}
	// WriteTimeout must exceed the per-request timeout.
	if srv.WriteTimeout <= requestTimeout {
		t.Errorf("WriteTimeout %v must exceed requestTimeout %v", srv.WriteTimeout, requestTimeout)
	}
	if srv.IdleTimeout != time.Minute {
		t.Errorf("IdleTimeout = %v, want 1m", srv.IdleTimeout)
	}
	if srv.Handler == nil {
		t.Fatal("Handler is nil")
	}

	// Wired, not just present. An unrouted path returns JSON.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

func TestServeShutsDownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- Serve(ctx, discardLogger(), lazyPool(t), "127.0.0.1:0") }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil after cancel", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

func TestServeReturnsListenError(t *testing.T) {
	// Hold the port so ListenAndServe cannot bind.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()

	// The listen failure alone must end Serve.
	err = Serve(context.Background(), discardLogger(), lazyPool(t), held.Addr().String())

	if err == nil {
		t.Fatal("Serve returned nil, want a listen error")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("error = %v, want it to mention listen", err)
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Error("ErrServerClosed leaked out of Serve")
	}
}
