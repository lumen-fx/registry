package src

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testOAuth(t *testing.T, handler http.HandlerFunc) githubOAuth {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return githubOAuth{
		clientID:     "id",
		clientSecret: "secret",
		authorizeURL: server.URL + "/authorize",
		tokenURL:     server.URL + "/token",
		userURL:      server.URL + "/user",
		client:       &http.Client{Timeout: time.Second},
	}
}

func TestGitHubExchange(t *testing.T) {
	ctx := context.Background()

	ok := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.FormValue("code") != "the-code" {
			t.Errorf("token request form = %v, want the code", r.Form)
		}
		fmt.Fprint(w, `{"access_token":"the-token"}`)
	})
	token, err := ok.exchange(ctx, "the-code")
	if err != nil || token != "the-token" {
		t.Errorf("exchange = %q, %v, want the-token", token, err)
	}

	down := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := down.exchange(ctx, "x"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("bad status err = %v, want the status code", err)
	}

	garbage := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	})
	if _, err := garbage.exchange(ctx, "x"); err == nil {
		t.Error("garbage body produced no error")
	}

	denied := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":"bad_verification_code"}`)
	})
	if _, err := denied.exchange(ctx, "x"); err == nil ||
		!strings.Contains(err.Error(), "bad_verification_code") {
		t.Errorf("denied err = %v, want github's error", err)
	}
}

func TestGitHubUser(t *testing.T) {
	ctx := context.Background()

	ok := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer the-token" {
			t.Errorf("auth = %q, want the bearer", got)
		}
		fmt.Fprint(w, `{"id":7,"login":"ada"}`)
	})
	id, login, err := ok.user(ctx, "the-token")
	if err != nil || id != 7 || login != "ada" {
		t.Errorf("user = %d, %q, %v, want 7 and ada", id, login, err)
	}

	down := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, _, err := down.user(ctx, "x"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("bad status err = %v, want the status code", err)
	}

	garbage := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	})
	if _, _, err := garbage.user(ctx, "x"); err == nil {
		t.Error("garbage body produced no error")
	}

	empty := testOAuth(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":0,"login":""}`)
	})
	if _, _, err := empty.user(ctx, "x"); err == nil ||
		!strings.Contains(err.Error(), "missing") {
		t.Errorf("empty identity err = %v, want the missing-fields error", err)
	}
}

// Without credentials the sign-in endpoints answer 503 and everything else
// stands; the cluster serves the read-only API before the secret exists.
func TestSignInUnconfigured(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GITHUB_CLIENT_SECRET", "")
	server := NewServer(nil)

	for _, path := range []string{"/auth/github/login", "/auth/github/callback"} {
		rec := httptest.NewRecorder()
		server.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, rec.Code)
		}
	}
}

func TestRequestIsSecure(t *testing.T) {
	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	if requestIsSecure(plain) {
		t.Error("a plain request counted as secure")
	}

	forwarded := httptest.NewRequest(http.MethodGet, "/", nil)
	forwarded.Header.Set("X-Forwarded-Proto", "https")
	if !requestIsSecure(forwarded) {
		t.Error("a forwarded https request counted as insecure")
	}
}

// The callback rejects a forged state before touching GitHub, so it needs no
// database and no provider.
func TestCallbackStateChecks(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "id")
	t.Setenv("GITHUB_CLIENT_SECRET", "secret")
	server := NewServer(nil)

	noCookie := httptest.NewRecorder()
	server.Routes().ServeHTTP(noCookie,
		httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=x&state=y", nil))
	if noCookie.Code != http.StatusForbidden {
		t.Errorf("no state cookie = %d, want 403", noCookie.Code)
	}

	missingCode := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=y", nil)
	missingCode.AddCookie(&http.Cookie{Name: stateCookie, Value: "y"})
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, missingCode)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing code = %d, want 400", rec.Code)
	}
}
