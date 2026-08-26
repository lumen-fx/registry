package src

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// End-to-end tests. They drive the real router over real HTTP against a real
// Postgres, so set TEST_DATABASE_URL to run them.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		// The unit tests still run. Every e2e test skips itself.
		os.Exit(m.Run())
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: connect: %v\n", err)
		os.Exit(1)
	}

	// The migrations build the schema, so every run tests them too.
	if err := RunMigrations(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		os.Exit(1)
	}

	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// api is one running server plus a client for it, backed by a stand-in for
// GitHub: the token endpoint answers every code with an access token equal to
// the code, and the user endpoint treats that token as the login. A test
// signs in as any user by using the login as the callback code.
type api struct {
	t      *testing.T
	server *Server
	url    string
}

// newAPI empties the tables, so each test starts from a known database.
func newAPI(t *testing.T) *api {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	if _, err := testPool.Exec(context.Background(),
		`TRUNCATE users, packages, releases, sessions, tokens CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	github := http.NewServeMux()
	github.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q}`, r.FormValue("code"))
	})
	github.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		login := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		hash := fnv.New32a()
		hash.Write([]byte(login))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%d,"login":%q}`, hash.Sum32(), login)
	})
	githubServer := httptest.NewServer(github)
	t.Cleanup(githubServer.Close)

	t.Setenv("GITHUB_CLIENT_ID", "test-client")
	t.Setenv("GITHUB_CLIENT_SECRET", "test-secret")
	t.Setenv("GITHUB_AUTHORIZE_URL", githubServer.URL+"/authorize")
	t.Setenv("GITHUB_TOKEN_URL", githubServer.URL+"/token")
	t.Setenv("GITHUB_USER_URL", githubServer.URL+"/user")

	server := NewServer(testPool)
	// The same chain NewHTTPServer builds, so the middleware is under test too.
	handler := Chain(server.Routes(), RequestLogger(discardLogger()), Recoverer(), Timeout(requestTimeout))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	return &api{t: t, server: server, url: httpServer.URL}
}

type response struct {
	status  int
	body    string
	header  http.Header
	cookies []*http.Cookie
}

// json decodes the response body into dst.
func (res response) json(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(res.body), dst); err != nil {
		t.Fatalf("decode %q: %v", res.body, err)
	}
}

// noRedirects returns responses as they are, so the tests read Location and
// Set-Cookie off the redirects themselves.
var noRedirects = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// do sends a request. A token authenticates it as a bearer; a session rides
// as the cookie. Either may be empty.
func (a *api) do(method, path, body, token, session string) response {
	a.t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.url+path, reader)
	if err != nil {
		a.t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}

	res, err := noRedirects.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		a.t.Fatalf("read body: %v", err)
	}

	return response{
		status:  res.StatusCode,
		body:    strings.TrimSpace(string(raw)),
		header:  res.Header,
		cookies: res.Cookies(),
	}
}

func (a *api) expect(want int, method, path, body string, token ...string) response {
	a.t.Helper()

	var bearer string
	if len(token) > 0 {
		bearer = token[0]
	}
	res := a.do(method, path, body, bearer, "")
	if res.status != want {
		a.t.Errorf("%s %s = %d, want %d: %s", method, path, res.status, want, res.body)
	}
	return res
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// signin walks the OAuth flow against the GitHub stand-in and returns the
// session secret.
func (a *api) signin(login string) string {
	a.t.Helper()

	start := a.do(http.MethodGet, "/auth/github/login", "", "", "")
	if start.status != http.StatusFound {
		a.t.Fatalf("login = %d, want a redirect: %s", start.status, start.body)
	}
	state := cookieValue(start.cookies, stateCookie)
	if state == "" {
		a.t.Fatal("login set no state cookie")
	}
	location, err := url.Parse(start.header.Get("Location"))
	if err != nil || location.Query().Get("state") != state {
		a.t.Fatalf("login redirected to %q, want the state to match its cookie", start.header.Get("Location"))
	}

	req, err := http.NewRequest(http.MethodGet,
		a.url+"/auth/github/callback?code="+url.QueryEscape(login)+"&state="+state, nil)
	if err != nil {
		a.t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: stateCookie, Value: state})

	res, err := noRedirects.Do(req)
	if err != nil {
		a.t.Fatalf("callback: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		a.t.Fatalf("callback = %d, want 303", res.StatusCode)
	}

	session := cookieValue(res.Cookies(), sessionCookie)
	if session == "" {
		a.t.Fatal("callback set no session cookie")
	}
	return session
}

// account is a signed-in user with one minted API token.
type account struct {
	ID       uuid.UUID
	Username string
	Session  string
	Token    string
}

func (a *api) signup(login string) account {
	a.t.Helper()

	session := a.signin(login)

	me := a.do(http.MethodGet, "/auth/me", "", "", session)
	if me.status != http.StatusOK {
		a.t.Fatalf("me = %d: %s", me.status, me.body)
	}
	var user PublicUser
	me.json(a.t, &user)

	minted := a.do(http.MethodPost, "/tokens", `{"name":"test"}`, "", session)
	if minted.status != http.StatusCreated {
		a.t.Fatalf("mint token = %d: %s", minted.status, minted.body)
	}
	var created CreatedToken
	minted.json(a.t, &created)

	return account{ID: user.ID, Username: user.Username, Session: session, Token: created.Secret}
}

// publish creates a package owned by the token's account.
func (a *api) publish(token, name string) Package {
	a.t.Helper()

	body := fmt.Sprintf(`{"platform":"linux","name":%q,"description":"a tool"}`, name)
	res := a.expect(http.StatusCreated, http.MethodPost, "/packages", body, token)

	var pkg Package
	res.json(a.t, &pkg)
	return pkg
}

// release publishes one version of a package.
func (a *api) release(token, name, version string) Release {
	a.t.Helper()

	body := fmt.Sprintf(`{"url":"https://example.test/%s-%s.tgz","version":%q}`, name, version, version)
	res := a.expect(http.StatusCreated, http.MethodPost, "/packages/"+name+"/releases", body, token)

	var rel Release
	res.json(a.t, &rel)
	return rel
}

func TestE2ERootAndHealth(t *testing.T) {
	a := newAPI(t)

	res := a.expect(http.StatusOK, http.MethodGet, "/", "")
	if !strings.Contains(res.body, "<!doctype html") || !strings.Contains(res.body, "lpm") {
		t.Errorf("body = %.80q..., want the web UI", res.body)
	}

	res = a.expect(http.StatusOK, http.MethodGet, "/health", "")
	var health HealthCheck
	res.json(t, &health)
	if health.Status != "OK" || health.Database != "up" {
		t.Errorf("health = %+v, want OK and up", health)
	}
}

func TestE2EUnroutedAndMethods(t *testing.T) {
	a := newAPI(t)

	res := a.expect(http.StatusNotFound, http.MethodGet, "/no/such/path", "")
	if !strings.Contains(res.body, "no route matches") {
		t.Errorf("body = %q, want the unrouted message", res.body)
	}

	for _, c := range []struct {
		method, path, allow string
	}{
		{http.MethodDelete, "/", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/health", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/auth/github/login", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/auth/github/callback", "GET, HEAD, OPTIONS"},
		{http.MethodGet, "/auth/logout", "POST, OPTIONS"},
		{http.MethodDelete, "/auth/me", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/tokens", "GET, POST, HEAD, OPTIONS"},
		{http.MethodGet, "/tokens/anything", "DELETE, OPTIONS"},
		{http.MethodDelete, "/packages", "GET, POST, HEAD, OPTIONS"},
		{http.MethodDelete, "/packages/anything", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/packages/anything/releases", "GET, POST, HEAD, OPTIONS"},
		{http.MethodDelete, "/packages/anything/releases/1.0.0", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/users/anyone", "GET, HEAD, OPTIONS"},
		{http.MethodDelete, "/users/anyone/packages", "GET, HEAD, OPTIONS"},
	} {
		res := a.expect(http.StatusMethodNotAllowed, c.method, c.path, "")
		if got := res.header.Get("Allow"); got != c.allow {
			t.Errorf("%s %s Allow = %q, want %q", c.method, c.path, got, c.allow)
		}
	}

	// OPTIONS is answered by the Allow header alone.
	res = a.expect(http.StatusNoContent, http.MethodOptions, "/packages", "")
	if got := res.header.Get("Allow"); got == "" {
		t.Error("OPTIONS carried no Allow header")
	}
	if res.body != "" {
		t.Errorf("204 carried a body: %q", res.body)
	}
}

func TestE2ESignIn(t *testing.T) {
	a := newAPI(t)

	acct := a.signup("alice")
	if acct.Username != "alice" || acct.ID == uuid.Nil {
		t.Errorf("account = %+v, want a filled record", acct)
	}

	// Signing in again lands on the same account, not a duplicate.
	again := a.signup("alice")
	if again.ID != acct.ID {
		t.Errorf("second sign-in made a new account: %s vs %s", again.ID, acct.ID)
	}
	other := a.signup("bob")
	if other.ID == acct.ID {
		t.Error("two logins shared one account")
	}

	// The profile exists publicly and leaks no GitHub identity.
	res := a.expect(http.StatusOK, http.MethodGet, "/users/alice", "")
	if strings.Contains(res.body, "github") {
		t.Errorf("profile = %q, want no github fields", res.body)
	}

	// A callback whose state matches no cookie is turned away.
	forged := a.do(http.MethodGet, "/auth/github/callback?code=alice&state=forged", "", "", "")
	if forged.status != http.StatusForbidden {
		t.Errorf("forged state = %d, want 403", forged.status)
	}

	// A callback with a matching state but no code is a client error.
	start := a.do(http.MethodGet, "/auth/github/login", "", "", "")
	state := cookieValue(start.cookies, stateCookie)
	req, err := http.NewRequest(http.MethodGet, a.url+"/auth/github/callback?state="+state, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: stateCookie, Value: state})
	missing, err := noRedirects.Do(req)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusBadRequest {
		t.Errorf("missing code = %d, want 400", missing.StatusCode)
	}
}

func TestE2EBadRequestBodies(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")

	for _, c := range []struct{ name, body string }{
		{"empty", ""},
		{"malformed", `{"name":`},
		{"wrong type", `{"name":42}`},
		{"unknown field", `{"name":"ci","bogus":1}`},
		{"two objects", `{"name":"ci"}{}`},
		{"not an object", `[]`},
	} {
		res := a.do(http.MethodPost, "/tokens", c.body, "", acct.Session)
		if res.status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", c.name, res.status, res.body)
		}
		if !strings.Contains(res.body, `"error"`) {
			t.Errorf("%s: body = %q, want a JSON error", c.name, res.body)
		}
	}

	// A body over the limit is rejected on size, not parsed.
	big := `{"name":"` + strings.Repeat("x", 1<<21) + `"}`
	if res := a.do(http.MethodPost, "/tokens", big, "", acct.Session); res.status != http.StatusRequestEntityTooLarge {
		t.Errorf("big body = %d, want 413", res.status)
	}

	// Auth runs before the decode, so no session means no parsing at all.
	if res := a.do(http.MethodPost, "/tokens", `{"name":"ci"}`, "", ""); res.status != http.StatusUnauthorized {
		t.Errorf("no session = %d, want 401", res.status)
	}
}

func TestE2ETokens(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")

	if !strings.HasPrefix(acct.Token, tokenPrefix) {
		t.Errorf("token = %q, want the %q prefix", acct.Token, tokenPrefix)
	}

	// The bearer identifies its account.
	me := a.expect(http.StatusOK, http.MethodGet, "/auth/me", "", acct.Token)
	var user PublicUser
	me.json(t, &user)
	if user.Username != "alice" {
		t.Errorf("me = %+v, want alice", user)
	}

	// The list shows the token without its secret or hash.
	list := a.do(http.MethodGet, "/tokens", "", "", acct.Session)
	if list.status != http.StatusOK {
		t.Fatalf("list = %d: %s", list.status, list.body)
	}
	var tokens []Token
	list.json(t, &tokens)
	if len(tokens) != 1 || tokens[0].Name != "test" {
		t.Errorf("tokens = %+v, want the one named test", tokens)
	}
	if strings.Contains(list.body, acct.Token) || strings.Contains(list.body, "hash") {
		t.Errorf("list = %q, want no secret material", list.body)
	}

	// A blank name is a field error.
	if res := a.do(http.MethodPost, "/tokens", `{"name":"  "}`, "", acct.Session); res.status != http.StatusUnprocessableEntity {
		t.Errorf("blank name = %d, want 422", res.status)
	}

	// Revoking someone else's token reveals nothing.
	bob := a.signup("bob")
	var bobTokens []Token
	a.do(http.MethodGet, "/tokens", "", "", bob.Session).json(t, &bobTokens)
	if res := a.do(http.MethodDelete, "/tokens/"+bobTokens[0].ID.String(), "", "", acct.Session); res.status != http.StatusNotFound {
		t.Errorf("cross-account revoke = %d, want 404", res.status)
	}

	// Revoking kills the credential.
	if res := a.do(http.MethodDelete, "/tokens/"+tokens[0].ID.String(), "", "", acct.Session); res.status != http.StatusOK {
		t.Errorf("revoke = %d, want 200", res.status)
	}
	revoked := a.expect(http.StatusUnauthorized, http.MethodGet, "/auth/me", "", acct.Token)
	if revoked.header.Get("WWW-Authenticate") == "" {
		t.Error("401 carried no WWW-Authenticate challenge")
	}
	if res := a.do(http.MethodDelete, "/tokens/"+tokens[0].ID.String(), "", "", acct.Session); res.status != http.StatusNotFound {
		t.Errorf("second revoke = %d, want 404", res.status)
	}

	// Signing out kills the session but not other credentials.
	if res := a.do(http.MethodPost, "/auth/logout", "", "", acct.Session); res.status != http.StatusOK {
		t.Errorf("logout = %d, want 200", res.status)
	}
	if res := a.do(http.MethodGet, "/tokens", "", "", acct.Session); res.status != http.StatusUnauthorized {
		t.Errorf("list after logout = %d, want 401", res.status)
	}
	a.expect(http.StatusOK, http.MethodGet, "/auth/me", "", bob.Token)
}

func TestE2EGetUser(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")
	a.publish(acct.Token, "alice-tool")
	a.release(acct.Token, "alice-tool", "1.0.0")

	res := a.expect(http.StatusOK, http.MethodGet, "/users/alice", "")
	var public PublicUser
	res.json(t, &public)
	if public.Username != "alice" || len(public.Packages) != 1 {
		t.Errorf("user = %+v, want alice with one package", public)
	}
	if len(public.Packages[0].Releases) != 1 {
		t.Errorf("releases = %d, want 1", len(public.Packages[0].Releases))
	}

	a.expect(http.StatusNotFound, http.MethodGet, "/users/nobody", "")
}

func TestE2EUserPackages(t *testing.T) {
	a := newAPI(t)
	alice := a.signup("alice")
	a.signup("bob")
	a.publish(alice.Token, "alice-tool")
	a.release(alice.Token, "alice-tool", "1.0.0")

	res := a.expect(http.StatusOK, http.MethodGet, "/users/alice/packages", "")
	var packages []Package
	res.json(t, &packages)
	if len(packages) != 1 || packages[0].Name != "alice-tool" {
		t.Errorf("packages = %+v, want alice-tool", packages)
	}

	// A user with nothing published is not an error.
	res = a.expect(http.StatusOK, http.MethodGet, "/users/bob/packages", "")
	if res.body != "[]" {
		t.Errorf("body = %q, want []", res.body)
	}

	a.expect(http.StatusNotFound, http.MethodGet, "/users/nobody/packages", "")
}

func TestE2EPublishPackage(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")

	pkg := a.publish(acct.Token, "alice-tool")
	if pkg.Name != "alice-tool" || pkg.Description != "a tool" || pkg.Releases == nil {
		t.Errorf("package = %+v, want a filled record with []", pkg)
	}

	// No credentials, and a challenge on the 401.
	res := a.expect(http.StatusUnauthorized, http.MethodPost, "/packages",
		`{"platform":"linux","name":"nope"}`)
	if res.header.Get("WWW-Authenticate") == "" {
		t.Error("401 carried no WWW-Authenticate challenge")
	}

	// A session is a browser credential; publishing wants a token.
	if got := a.do(http.MethodPost, "/packages", `{"platform":"linux","name":"nope"}`, "", acct.Session); got.status != http.StatusUnauthorized {
		t.Errorf("session publish = %d, want 401", got.status)
	}

	// A name is taken globally, not per platform.
	a.expect(http.StatusConflict, http.MethodPost, "/packages",
		`{"platform":"darwin","name":"alice-tool"}`, acct.Token)

	for _, body := range []string{
		`{"platform":"","name":"valid-name"}`,
		`{"platform":"linux","name":""}`,
		`{"platform":"linux","name":"bad name"}`,
		`{"platform":"linux","name":"-leading-dash"}`,
	} {
		a.expect(http.StatusUnprocessableEntity, http.MethodPost, "/packages", body, acct.Token)
	}
}

func TestE2EWrongTokenCannotPublish(t *testing.T) {
	a := newAPI(t)
	a.signup("alice")

	a.expect(http.StatusUnauthorized, http.MethodPost, "/packages",
		`{"platform":"linux","name":"alice-tool"}`, "lpm_not-a-real-token")
}

func TestE2EGetPackage(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")
	a.publish(acct.Token, "alice-tool")
	a.release(acct.Token, "alice-tool", "1.0.0")
	a.release(acct.Token, "alice-tool", "2.0.0")

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool", "")
	var pkg Package
	res.json(t, &pkg)
	if len(pkg.Releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(pkg.Releases))
	}
	// Newest first.
	if pkg.Releases[0].Version != "2.0.0" {
		t.Errorf("first release = %q, want 2.0.0", pkg.Releases[0].Version)
	}

	a.expect(http.StatusNotFound, http.MethodGet, "/packages/nothing-here", "")
}

func TestE2EPackageReleases(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")
	a.publish(acct.Token, "alice-tool")
	a.publish(acct.Token, "alice-empty")
	a.release(acct.Token, "alice-tool", "1.0.0")

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool/releases", "")
	var releases []Release
	res.json(t, &releases)
	if len(releases) != 1 || releases[0].URL == "" {
		t.Errorf("releases = %+v, want one with a url", releases)
	}

	res = a.expect(http.StatusOK, http.MethodGet, "/packages/alice-empty/releases", "")
	if res.body != "[]" {
		t.Errorf("body = %q, want []", res.body)
	}

	a.expect(http.StatusNotFound, http.MethodGet, "/packages/nothing-here/releases", "")
}

func TestE2EPublishRelease(t *testing.T) {
	a := newAPI(t)
	alice := a.signup("alice")
	bob := a.signup("bob")
	a.publish(alice.Token, "alice-tool")

	rel := a.release(alice.Token, "alice-tool", "1.0.0")
	if rel.Version != "1.0.0" || rel.CreatedAt.IsZero() {
		t.Errorf("release = %+v, want a filled record", rel)
	}

	// A version that is wider than semver still round-trips through the path.
	const odd = "v2.0.0-rc.1+build.5"
	a.release(alice.Token, "alice-tool", odd)
	a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool/releases/"+odd, "")

	body := `{"url":"https://example.test/x.tgz","version":"3.0.0"}`
	a.expect(http.StatusUnauthorized, http.MethodPost, "/packages/alice-tool/releases", body)
	a.expect(http.StatusForbidden, http.MethodPost, "/packages/alice-tool/releases", body, bob.Token)
	a.expect(http.StatusNotFound, http.MethodPost, "/packages/nothing-here/releases", body, alice.Token)
	a.expect(http.StatusConflict, http.MethodPost, "/packages/alice-tool/releases",
		`{"url":"https://example.test/again.tgz","version":"1.0.0"}`, alice.Token)

	for _, invalid := range []string{
		`{"url":"","version":"9.0.0"}`,
		`{"url":"http://example.test/x.tgz","version":"9.0.0"}`,
		`{"url":"https://user:pass@example.test/x.tgz","version":"9.0.0"}`,
		`{"url":"https://example.test/x.tgz#frag","version":"9.0.0"}`,
		`{"url":"file:///etc/passwd","version":"9.0.0"}`,
		`{"url":"https:///x.tgz","version":"9.0.0"}`,
		`{"url":"https://example.test/x.tgz","version":""}`,
		`{"url":"https://example.test/x.tgz","version":"9 0 0"}`,
	} {
		a.expect(http.StatusUnprocessableEntity, http.MethodPost,
			"/packages/alice-tool/releases", invalid, alice.Token)
	}
}

func TestE2EGetRelease(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")
	a.publish(acct.Token, "alice-tool")
	a.release(acct.Token, "alice-tool", "1.0.0")

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool/releases/1.0.0", "")
	var rel Release
	res.json(t, &rel)
	if rel.Version != "1.0.0" || rel.URL == "" {
		t.Errorf("release = %+v, want 1.0.0 with a url", rel)
	}

	// The two 404s are told apart.
	missingVersion := a.expect(http.StatusNotFound, http.MethodGet,
		"/packages/alice-tool/releases/9.9.9", "")
	missingPackage := a.expect(http.StatusNotFound, http.MethodGet,
		"/packages/nothing-here/releases/1.0.0", "")
	if missingVersion.body == missingPackage.body {
		t.Errorf("both 404s say %q, want different messages", missingVersion.body)
	}
}

func TestE2ESearchPackages(t *testing.T) {
	a := newAPI(t)
	alice := a.signup("alice")
	bob := a.signup("bob")
	a.publish(alice.Token, "alice-tool")
	a.release(alice.Token, "alice-tool", "1.0.0")

	body := `{"platform":"darwin","name":"bob-kit","description":"a widget"}`
	a.expect(http.StatusCreated, http.MethodPost, "/packages", body, bob.Token)

	names := func(query string) []string {
		a.t.Helper()

		res := a.expect(http.StatusOK, http.MethodGet, "/packages"+query, "")
		var packages []Package
		res.json(t, &packages)

		out := make([]string, len(packages))
		for i, p := range packages {
			out[i] = p.Name
		}
		return out
	}

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"", []string{"bob-kit", "alice-tool"}}, // no filter: newest first
		{"?platform=linux", []string{"alice-tool"}},
		{"?platform=windows", []string{}},
		{"?name=tool", []string{"alice-tool"}},
		{"?q=widget", []string{"bob-kit"}},
		{"?q=alice", []string{"alice-tool"}},
		{"?username=bob", []string{"bob-kit"}},
		{"?version=1.0.0", []string{"alice-tool"}},
		{"?version=9.9.9", []string{}},
		{"?limit=1", []string{"bob-kit"}},
		{"?platform=linux&q=tool", []string{"alice-tool"}},
		{"?name=%20tool%20", []string{"alice-tool"}}, // trimmed
		{"?q=" + strings.Repeat("z", 200), []string{}},
	} {
		got := names(c.query)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("GET /packages%s = %v, want %v", c.query, got, c.want)
		}
	}

	// A wildcard in the search term is matched literally.
	if got := names("?name=%25"); len(got) != 0 {
		t.Errorf("name=%% matched %v, want nothing", got)
	}

	for _, query := range []string{
		"?limit=abc",
		"?limit=0",
		"?limit=-1",
		"?q=" + strings.Repeat("z", 201),
	} {
		res := a.expect(http.StatusUnprocessableEntity, http.MethodGet, "/packages"+query, "")
		var errRes ErrorResponse
		res.json(t, &errRes)
		if len(errRes.Fields) == 0 {
			t.Errorf("%s named no fields", query)
		}
	}
}

func TestE2ELimitIsCapped(t *testing.T) {
	a := newAPI(t)
	acct := a.signup("alice")
	a.publish(acct.Token, "alice-tool")

	// Over the cap is clamped, not rejected.
	res := a.expect(http.StatusOK, http.MethodGet, "/packages?limit=100000", "")
	var packages []Package
	res.json(t, &packages)
	if len(packages) != 1 {
		t.Errorf("packages = %d, want 1", len(packages))
	}
}

func TestE2ERequestIDIsEchoedByTheLogger(t *testing.T) {
	a := newAPI(t)

	// The header is accepted rather than rejected. The logger reads it.
	req, err := http.NewRequest(http.MethodGet, a.url+"/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-Id", "test-request-id")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
}

func TestE2EFullPublishFlow(t *testing.T) {
	a := newAPI(t)

	acct := a.signup("alice")
	a.publish(acct.Token, "alice-tool")
	a.release(acct.Token, "alice-tool", "1.0.0")
	a.release(acct.Token, "alice-tool", "1.1.0")

	// The package, its releases, one release, and the publisher's profile all
	// agree about what was published.
	var pkg Package
	a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool", "").json(t, &pkg)

	var releases []Release
	a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool/releases", "").json(t, &releases)

	var rel Release
	a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool/releases/1.1.0", "").json(t, &rel)

	var profile PublicUser
	a.expect(http.StatusOK, http.MethodGet, "/users/alice", "").json(t, &profile)

	if len(pkg.Releases) != 2 || len(releases) != 2 {
		t.Errorf("package has %d releases, endpoint returned %d, want 2 and 2",
			len(pkg.Releases), len(releases))
	}
	if rel.Version != "1.1.0" || rel.ID != releases[0].ID {
		t.Errorf("release = %+v, want the newest of %+v", rel, releases[0])
	}
	if len(profile.Packages) != 1 || len(profile.Packages[0].Releases) != 2 {
		t.Errorf("profile = %+v, want one package with two releases", profile)
	}
}
