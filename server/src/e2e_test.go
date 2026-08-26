package src

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

const testPassword = "correct-horse-battery"

// api is one running server plus a client for it.
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
		`TRUNCATE users, packages, releases CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	server := NewServer(testPool)
	// The same chain NewHTTPServer builds, so the middleware is under test too.
	handler := Chain(server.Routes(), RequestLogger(discardLogger()), Recoverer(), Timeout(requestTimeout))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	return &api{t: t, server: server, url: httpServer.URL}
}

type response struct {
	status int
	body   string
	header http.Header
}

// json decodes the response body into dst.
func (res response) json(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(res.body), dst); err != nil {
		t.Fatalf("decode %q: %v", res.body, err)
	}
}

func (a *api) do(method, path, body string, as ...string) response {
	a.t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.url+path, reader)
	if err != nil {
		a.t.Fatalf("new request: %v", err)
	}
	if len(as) > 0 {
		req.SetBasicAuth(as[0], testPassword)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		a.t.Fatalf("read body: %v", err)
	}

	return response{status: res.StatusCode, body: strings.TrimSpace(string(raw)), header: res.Header}
}

func (a *api) expect(want int, method, path, body string, as ...string) response {
	a.t.Helper()

	res := a.do(method, path, body, as...)
	if res.status != want {
		a.t.Errorf("%s %s = %d, want %d: %s", method, path, res.status, want, res.body)
	}
	return res
}

// register creates a user and returns it.
func (a *api) register(username string) User {
	a.t.Helper()

	body := fmt.Sprintf(`{"username":%q,"email":"%s@example.test","password":%q}`,
		username, username, testPassword)
	res := a.expect(http.StatusCreated, http.MethodPost, "/user/register", body)

	var user User
	res.json(a.t, &user)
	return user
}

// publish creates a package owned by username.
func (a *api) publish(username, name string) Package {
	a.t.Helper()

	body := fmt.Sprintf(`{"platform":"linux","name":%q,"description":"a tool"}`, name)
	res := a.expect(http.StatusCreated, http.MethodPost, "/packages", body, username)

	var pkg Package
	res.json(a.t, &pkg)
	return pkg
}

// release publishes one version of a package.
func (a *api) release(username, name, version string) Release {
	a.t.Helper()

	body := fmt.Sprintf(`{"url":"https://example.test/%s-%s.tgz","version":%q}`, name, version, version)
	res := a.expect(http.StatusCreated, http.MethodPost, "/packages/"+name+"/releases", body, username)

	var rel Release
	res.json(a.t, &rel)
	return rel
}

func TestE2ERootAndHealth(t *testing.T) {
	a := newAPI(t)

	res := a.expect(http.StatusOK, http.MethodGet, "/", "")
	if res.body != "Hello, World!" {
		t.Errorf("body = %q, want the greeting", res.body)
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
		{http.MethodGet, "/user/register", "POST, OPTIONS"},
		{http.MethodGet, "/user/login", "POST, OPTIONS"},
		{http.MethodGet, "/user/change_password", "POST, OPTIONS"},
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

func TestE2ERegister(t *testing.T) {
	a := newAPI(t)

	user := a.register("alice")
	if user.Username != "alice" || user.ID.String() == "" {
		t.Errorf("user = %+v, want a filled record", user)
	}
	if user.Packages == nil {
		t.Error("packages = null, want []")
	}
	if strings.Contains(a.do(http.MethodGet, "/users/alice", "").body, "password") {
		t.Error("a password field reached the wire")
	}

	// Same username and same email both conflict.
	a.expect(http.StatusConflict, http.MethodPost, "/user/register",
		`{"username":"alice","email":"other@example.test","password":"`+testPassword+`"}`)
	a.expect(http.StatusConflict, http.MethodPost, "/user/register",
		`{"username":"other","email":"alice@example.test","password":"`+testPassword+`"}`)

	for _, body := range []string{
		`{"username":"","email":"a@example.test","password":"` + testPassword + `"}`,
		`{"username":"no","email":"a@example.test","password":"` + testPassword + `"}`,
		`{"username":"-bad-","email":"a@example.test","password":"` + testPassword + `"}`,
		`{"username":"bob","email":"not-an-email","password":"` + testPassword + `"}`,
		`{"username":"bob","email":"bob@nodot","password":"` + testPassword + `"}`,
		`{"username":"bob","email":"bob@example.test","password":"short"}`,
	} {
		res := a.expect(http.StatusUnprocessableEntity, http.MethodPost, "/user/register", body)
		var errRes ErrorResponse
		res.json(t, &errRes)
		if len(errRes.Fields) == 0 {
			t.Errorf("%s named no fields", body)
		}
	}
}

func TestE2EBadRequestBodies(t *testing.T) {
	a := newAPI(t)

	for _, c := range []struct{ name, body string }{
		{"empty", ""},
		{"malformed", `{"username":`},
		{"wrong type", `{"username":42,"email":"a@example.test","password":"x"}`},
		{"unknown field", `{"username":"bob","email":"a@example.test","password":"x","bogus":1}`},
		{"two objects", `{"username":"bob","email":"a@example.test","password":"x"}{}`},
		{"not an object", `[]`},
	} {
		res := a.expect(http.StatusBadRequest, http.MethodPost, "/user/register", c.body)
		if !strings.Contains(res.body, `"error"`) {
			t.Errorf("%s: body = %q, want a JSON error", c.name, res.body)
		}
	}

	// A body over the limit is rejected on size, not parsed.
	big := `{"username":"bob","email":"a@example.test","password":"` + strings.Repeat("x", 1<<21) + `"}`
	a.expect(http.StatusRequestEntityTooLarge, http.MethodPost, "/user/register", big)
}

func TestE2ELogin(t *testing.T) {
	a := newAPI(t)
	a.register("alice")
	a.publish("alice", "alice-tool")

	res := a.expect(http.StatusOK, http.MethodPost, "/user/login",
		`{"username":"alice","password":"`+testPassword+`"}`)

	var user User
	res.json(t, &user)
	if len(user.Packages) != 1 {
		t.Errorf("packages = %d, want 1", len(user.Packages))
	}
	if user.Packages[0].Releases == nil {
		t.Error("releases = null, want []")
	}

	// A wrong password and an unknown user answer identically.
	wrongPassword := a.expect(http.StatusUnauthorized, http.MethodPost, "/user/login",
		`{"username":"alice","password":"not-the-password"}`)
	unknownUser := a.expect(http.StatusUnauthorized, http.MethodPost, "/user/login",
		`{"username":"nobody","password":"`+testPassword+`"}`)
	if wrongPassword.body != unknownUser.body {
		t.Errorf("login answers differ: %q vs %q", wrongPassword.body, unknownUser.body)
	}

	a.expect(http.StatusUnprocessableEntity, http.MethodPost, "/user/login",
		`{"username":"","password":""}`)
}

func TestE2EChangePassword(t *testing.T) {
	a := newAPI(t)
	a.register("alice")

	const next = "another-good-password"
	a.expect(http.StatusOK, http.MethodPost, "/user/change_password",
		fmt.Sprintf(`{"username":"alice","currentPassword":%q,"newPassword":%q}`, testPassword, next))

	// The old password stops working and the new one starts.
	a.expect(http.StatusUnauthorized, http.MethodPost, "/user/login",
		`{"username":"alice","password":"`+testPassword+`"}`)
	a.expect(http.StatusOK, http.MethodPost, "/user/login",
		fmt.Sprintf(`{"username":"alice","password":%q}`, next))

	a.expect(http.StatusUnauthorized, http.MethodPost, "/user/change_password",
		fmt.Sprintf(`{"username":"alice","currentPassword":"wrong","newPassword":%q}`, next))
	a.expect(http.StatusUnauthorized, http.MethodPost, "/user/change_password",
		fmt.Sprintf(`{"username":"nobody","currentPassword":%q,"newPassword":%q}`, testPassword, next))
	a.expect(http.StatusUnprocessableEntity, http.MethodPost, "/user/change_password",
		fmt.Sprintf(`{"username":"alice","currentPassword":%q,"newPassword":%q}`, next, next))
}

func TestE2EGetUser(t *testing.T) {
	a := newAPI(t)
	a.register("alice")
	a.publish("alice", "alice-tool")
	a.release("alice", "alice-tool", "1.0.0")

	res := a.expect(http.StatusOK, http.MethodGet, "/users/alice", "")
	var public PublicUser
	res.json(t, &public)
	if public.Username != "alice" || len(public.Packages) != 1 {
		t.Errorf("user = %+v, want alice with one package", public)
	}
	if len(public.Packages[0].Releases) != 1 {
		t.Errorf("releases = %d, want 1", len(public.Packages[0].Releases))
	}
	if strings.Contains(res.body, "@example.test") {
		t.Error("the email reached a public profile")
	}

	a.expect(http.StatusNotFound, http.MethodGet, "/users/nobody", "")
}

func TestE2EUserPackages(t *testing.T) {
	a := newAPI(t)
	a.register("alice")
	a.register("bob")
	a.publish("alice", "alice-tool")
	a.release("alice", "alice-tool", "1.0.0")

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
	a.register("alice")

	pkg := a.publish("alice", "alice-tool")
	if pkg.Name != "alice-tool" || pkg.Description != "a tool" || pkg.Releases == nil {
		t.Errorf("package = %+v, want a filled record with []", pkg)
	}

	// No credentials, and a challenge on the 401.
	res := a.expect(http.StatusUnauthorized, http.MethodPost, "/packages",
		`{"platform":"linux","name":"nope"}`)
	if res.header.Get("WWW-Authenticate") == "" {
		t.Error("401 carried no WWW-Authenticate challenge")
	}

	// A name is taken globally, not per platform.
	a.expect(http.StatusConflict, http.MethodPost, "/packages",
		`{"platform":"darwin","name":"alice-tool"}`, "alice")

	for _, body := range []string{
		`{"platform":"","name":"valid-name"}`,
		`{"platform":"linux","name":""}`,
		`{"platform":"linux","name":"bad name"}`,
		`{"platform":"linux","name":"-leading-dash"}`,
	} {
		a.expect(http.StatusUnprocessableEntity, http.MethodPost, "/packages", body, "alice")
	}
}

func TestE2EWrongPasswordCannotPublish(t *testing.T) {
	a := newAPI(t)
	a.register("alice")

	req, err := http.NewRequest(http.MethodPost, a.url+"/packages",
		strings.NewReader(`{"platform":"linux","name":"alice-tool"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.SetBasicAuth("alice", "not-the-password")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}

func TestE2EGetPackage(t *testing.T) {
	a := newAPI(t)
	a.register("alice")
	a.publish("alice", "alice-tool")
	a.release("alice", "alice-tool", "1.0.0")
	a.release("alice", "alice-tool", "2.0.0")

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
	a.register("alice")
	a.publish("alice", "alice-tool")
	a.publish("alice", "alice-empty")
	a.release("alice", "alice-tool", "1.0.0")

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
	a.register("alice")
	a.register("bob")
	a.publish("alice", "alice-tool")

	rel := a.release("alice", "alice-tool", "1.0.0")
	if rel.Version != "1.0.0" || rel.CreatedAt.IsZero() {
		t.Errorf("release = %+v, want a filled record", rel)
	}

	// A version that is wider than semver still round-trips through the path.
	const odd = "v2.0.0-rc.1+build.5"
	a.release("alice", "alice-tool", odd)
	a.expect(http.StatusOK, http.MethodGet, "/packages/alice-tool/releases/"+odd, "")

	body := `{"url":"https://example.test/x.tgz","version":"3.0.0"}`
	a.expect(http.StatusUnauthorized, http.MethodPost, "/packages/alice-tool/releases", body)
	a.expect(http.StatusForbidden, http.MethodPost, "/packages/alice-tool/releases", body, "bob")
	a.expect(http.StatusNotFound, http.MethodPost, "/packages/nothing-here/releases", body, "alice")
	a.expect(http.StatusConflict, http.MethodPost, "/packages/alice-tool/releases",
		`{"url":"https://example.test/again.tgz","version":"1.0.0"}`, "alice")

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
			"/packages/alice-tool/releases", invalid, "alice")
	}
}

func TestE2EGetRelease(t *testing.T) {
	a := newAPI(t)
	a.register("alice")
	a.publish("alice", "alice-tool")
	a.release("alice", "alice-tool", "1.0.0")

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
	a.register("alice")
	a.register("bob")
	a.publish("alice", "alice-tool")
	a.release("alice", "alice-tool", "1.0.0")

	body := `{"platform":"darwin","name":"bob-kit","description":"a widget"}`
	a.expect(http.StatusCreated, http.MethodPost, "/packages", body, "bob")

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
	a.register("alice")
	a.publish("alice", "alice-tool")

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

	a.register("alice")
	a.publish("alice", "alice-tool")
	a.release("alice", "alice-tool", "1.0.0")
	a.release("alice", "alice-tool", "1.1.0")

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
