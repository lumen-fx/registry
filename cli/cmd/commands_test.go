package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// run executes the root command with a fresh output buffer and the given
// stdin, returning what it printed and the error.
func run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

// fakeRegistry accepts the token "lpm_good" for the endpoints the commands
// use.
func fakeRegistry(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer lpm_good" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"a valid API token is required"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /auth/me", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			fmt.Fprint(w, `{"id":"11111111-1111-1111-1111-111111111111","username":"ada","packages":[]}`)
		}
	})
	mux.HandleFunc("POST /packages", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"22222222-2222-2222-2222-222222222222","platform":"lumen","name":"lantern","releases":[]}`)
		}
	})
	mux.HandleFunc("POST /packages/lantern/releases", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"33333333-3333-3333-3333-333333333333","url":"https://example.test/l.tgz","version":"1.0.0"}`)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestLoginPublishReleaseFlow(t *testing.T) {
	t.Setenv("LPM_CONFIG_DIR", t.TempDir())
	registry := fakeRegistry(t)

	out, err := run(t, "lpm_good\n", "login", "--registry", registry.URL)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "Signed in") || !strings.Contains(out, "ada") {
		t.Errorf("login output = %q, want the account name", out)
	}

	out, err = run(t, "", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(out, "ada") {
		t.Errorf("whoami output = %q, want ada", out)
	}

	out, err = run(t, "", "publish", "lantern", "--platform", "lumen", "--description", "a lamp")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !strings.Contains(out, "Published lantern") {
		t.Errorf("publish output = %q", out)
	}

	out, err = run(t, "", "release", "lantern", "1.0.0", "--url", "https://example.test/l.tgz")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !strings.Contains(out, "Released lantern 1.0.0") {
		t.Errorf("release output = %q", out)
	}

	if _, err := run(t, "", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := run(t, "", "whoami"); err == nil ||
		!strings.Contains(err.Error(), "lpm login") {
		t.Errorf("whoami after logout = %v, want a pointer at login", err)
	}
}

func TestLoginRejectsABadToken(t *testing.T) {
	t.Setenv("LPM_CONFIG_DIR", t.TempDir())
	registry := fakeRegistry(t)

	if _, err := run(t, "lpm_wrong\n", "login", "--registry", registry.URL); err == nil ||
		!strings.Contains(err.Error(), "did not authenticate") {
		t.Errorf("login = %v, want an authentication failure", err)
	}
	if _, err := run(t, "\n", "login", "--registry", registry.URL); err == nil ||
		!strings.Contains(err.Error(), "no token") {
		t.Errorf("empty login = %v, want the empty-token error", err)
	}
}

func TestPublishWithoutLoginPointsAtLogin(t *testing.T) {
	t.Setenv("LPM_CONFIG_DIR", t.TempDir())

	if _, err := run(t, "", "publish", "lantern", "--platform", "lumen"); err == nil ||
		!strings.Contains(err.Error(), "lpm login") {
		t.Errorf("publish = %v, want a pointer at login", err)
	}
}
