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
	if !strings.HasPrefix(body, "#!/bin/sh") || !strings.Contains(body, "releases/latest") {
		t.Errorf("body = %.80q..., want the installer script", body)
	}
}

// The README panel renders markdown in the browser, so the libraries that do
// it are served from the binary rather than a CDN. They are versioned in their
// name and cached accordingly.
func TestAssetsAreServed(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/marked-18.0.13.esm.js", nil)
	req.SetPathValue("asset", "marked-18.0.13.esm.js")
	NewServer(nil).AssetHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want an immutable asset", cc)
	}
	if rec.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

// Only the scripts are reachable: the licences ship in the repository, and a
// name that is not there is a 404 rather than a server error.
func TestUnknownAssetsAreNotFound(t *testing.T) {
	for _, name := range []string{"marked-18.0.13.LICENSE", "nothing.js", "../index.html"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assets/x", nil)
		req.SetPathValue("asset", name)
		NewServer(nil).AssetHandler(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", name, rec.Code)
		}
	}
}
