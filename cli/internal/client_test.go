package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeRegistry accepts the token "lpm_good" and mirrors the registry's JSON
// shapes for the endpoints the client uses.
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
		if !authed(w, r) {
			return
		}
		fmt.Fprint(w, `{"id":"11111111-1111-1111-1111-111111111111","username":"ada","packages":[]}`)
	})
	mux.HandleFunc("POST /packages", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"22222222-2222-2222-2222-222222222222","platform":"lumen","name":"lantern","releases":[]}`)
	})
	mux.HandleFunc("POST /packages/lantern/releases", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"33333333-3333-3333-3333-333333333333","url":"https://example.test/l.tgz","version":"1.0.0"}`)
	})
	mux.HandleFunc("POST /packages/broken/releases", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"error":"request contains invalid fields","fields":{"url":"must use https","version":"is required"}}`)
	})
	mux.HandleFunc("POST /packages/dead/releases", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "not json")
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func testClient(t *testing.T, token string) *Client {
	return NewClient(Config{Registry: fakeRegistry(t).URL, Token: token})
}

func TestClientMe(t *testing.T) {
	user, err := testClient(t, "lpm_good").Me()
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "ada" {
		t.Errorf("user = %+v, want ada", user)
	}

	if _, err := testClient(t, "lpm_bad").Me(); err == nil ||
		!strings.Contains(err.Error(), "valid API token") {
		t.Errorf("bad token err = %v, want the registry's message", err)
	}
}

func TestClientCreatePackageAndRelease(t *testing.T) {
	c := testClient(t, "lpm_good")

	pkg, err := c.CreatePackage(NewPackage{Platform: "lumen", Name: "lantern"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "lantern" {
		t.Errorf("package = %+v, want lantern", pkg)
	}

	rel, err := c.CreateRelease("lantern", NewRelease{URL: "https://example.test/l.tgz", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.0.0" {
		t.Errorf("release = %+v, want 1.0.0", rel)
	}
}

func TestClientReportsFieldErrors(t *testing.T) {
	_, err := testClient(t, "lpm_good").CreateRelease("broken", NewRelease{})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, part := range []string{"invalid fields", "url: must use https", "version: is required"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("err = %q, want it to contain %q", err, part)
		}
	}
}

func TestClientReportsNonJSONFailures(t *testing.T) {
	_, err := testClient(t, "lpm_good").CreateRelease("dead", NewRelease{})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want the status code", err)
	}
}
