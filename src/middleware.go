package src

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type Middleware func(http.Handler) http.Handler

func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (rec *responseRecorder) WriteHeader(code int) {
	if rec.wroteHeader {
		return // swallow the duplicate; net/http would log "superfluous WriteHeader"
	}
	rec.wroteHeader = true
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *responseRecorder) Write(b []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += int64(n)
	return n, err
}

func (rec *responseRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func (rec *responseRecorder) WroteHeader() bool { return rec.wroteHeader }

// responseStarter reports whether the response has already begun.
type responseStarter interface{ WroteHeader() bool }

type loggerKey struct{}

func ContextWithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func RequestLogger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

			reqLogger := logger.With(
				slog.String("requestId", requestID(r)),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
			r = r.WithContext(ContextWithLogger(r.Context(), reqLogger))

			defer func() {
				level := slog.LevelInfo
				switch {
				case rec.status >= 500:
					level = slog.LevelError
				case rec.status >= 400:
					level = slog.LevelWarn
				}
				reqLogger.LogAttrs(r.Context(), level, "request completed",
					slog.Int("status", rec.status),
					slog.Int64("bytes", rec.bytes),
					slog.Duration("duration", time.Since(start)),
					slog.String("remoteAddr", r.RemoteAddr),
				)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

// Timeout bounds a request so a stalled query is cancelled instead of holding
// a pool connection until the client gives up.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func Recoverer() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					if errors.Is(r.Context().Err(), context.Canceled) {
						return // client hung up mid-write, not our bug
					}
					LoggerFrom(r.Context()).Error("panic in handler",
						slog.Any("panic", rv),
						slog.String("stack", string(debug.Stack())))
					// Partial response already sent; appending would corrupt it.
					if started, ok := w.(responseStarter); ok && started.WroteHeader() {
						return
					}
					writeError(w, r, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
