package src

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Both handlers are static, so they are exercised without a database: the UI
// doubles as the liveness endpoint and must stay reachable when Postgres is
// not.
func TestRootServesTheUI(t *testing.T) {
	rec := httptest.NewRecorder()
	NewServer(nil).RootHandler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!doctype html") || !strings.Contains(body, "lpm") {
		t.Errorf("body = %.80q..., want the web UI", body)
	}
}

func TestInstallScriptIsServed(t *testing.T) {
	rec := httptest.NewRecorder()
	NewServer(nil).InstallScriptHandler(rec, httptest.NewRequest(http.MethodGet, "/install.sh", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "#!/bin/sh") || !strings.Contains(body, "/packages/lpm/releases") {
		t.Errorf("body = %.80q..., want the installer script", body)
	}
}
