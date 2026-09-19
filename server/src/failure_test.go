package src

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// deadPool returns a pool whose queries all fail, so the error paths that a
// working database never reaches can be tested.
func deadPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool := lazyPool(t)
	pool.Close()
	return pool
}

// TestDatabaseFailuresAnswer500 walks every route that talks to the database
// and checks it reports a server error instead of panicking.
func TestDatabaseFailuresAnswer500(t *testing.T) {
	server := NewServer(deadPool(t))
	handler := Chain(server.Routes(), RequestLogger(discardLogger()), Recoverer(), Timeout(requestTimeout))

	for _, c := range []struct {
		method, path, body string
		auth               bool
		want               int
	}{
		{http.MethodGet, "/health", "", false, http.StatusServiceUnavailable},
		{http.MethodGet, "/users/alice", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/users/alice/packages", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/packages", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/packages/alice-tool", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/packages/alice-tool/releases", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/packages/alice-tool/releases/1.0.0", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/packages/alice-tool/readme", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/packages/alice-tool/downloads", "", false, http.StatusInternalServerError},
		{http.MethodGet, "/auth/me", "", true, http.StatusInternalServerError},
		{http.MethodPost, "/auth/logout", "", true, http.StatusInternalServerError},
		{http.MethodGet, "/tokens", "", true, http.StatusInternalServerError},
		{http.MethodPost, "/tokens", `{"name":"ci"}`, true, http.StatusInternalServerError},
		{http.MethodDelete, "/tokens/11111111-1111-1111-1111-111111111111", "", true, http.StatusInternalServerError},
		{http.MethodPost, "/packages", `{"platform":"lumen","name":"alice-tool"}`, true, http.StatusInternalServerError},
		{http.MethodDelete, "/packages/alice-tool", "", true, http.StatusInternalServerError},
		{http.MethodPost, "/packages/alice-tool/releases", `{"version":"1.0.0","artifacts":[{"target":"any","url":"https://example.test/x.tgz","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","size":4}]}`, true, http.StatusInternalServerError},
	} {
		var body *strings.Reader
		if c.body == "" {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(c.body)
		}

		req := httptest.NewRequest(c.method, c.path, body)
		if c.auth {
			req.Header.Set("Authorization", "Bearer lpm_deadbeef")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "deadbeef"})
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
			t.Errorf("%s %s Content-Type = %q, want JSON", c.method, c.path, ct)
		}
	}
}

// TestStoreFailuresAreWrapped checks the store reports a failed query rather
// than an empty result.
func TestStoreFailuresAreWrapped(t *testing.T) {
	s := NewServer(deadPool(t))
	ctx := context.Background()

	publisher := User{Username: "alice"}
	packaged := Package{Name: "alice-tool"}

	calls := []struct {
		name string
		err  error
	}{}
	add := func(name string, err error) {
		calls = append(calls, struct {
			name string
			err  error
		}{name, err})
	}

	_, err := s.upsertGitHubUser(ctx, 1, "alice")
	add("upsertGitHubUser", err)

	_, err = s.getUser(ctx, "alice")
	add("getUser", err)

	_, err = s.createSession(ctx, publisher.ID)
	add("createSession", err)

	_, err = s.sessionUser(ctx, "deadbeef")
	add("sessionUser", err)

	add("deleteSession", s.deleteSession(ctx, "deadbeef"))

	_, err = s.createToken(ctx, publisher.ID, "ci")
	add("createToken", err)

	_, err = s.listTokens(ctx, publisher.ID)
	add("listTokens", err)

	add("revokeToken", s.revokeToken(ctx, publisher.ID, publisher.ID))

	_, err = s.tokenUser(ctx, "lpm_deadbeef")
	add("tokenUser", err)

	_, err = s.listPackages(ctx, PackageFilter{})
	add("listPackages", err)

	_, err = s.getPackage(ctx, "alice-tool")
	add("getPackage", err)

	_, err = s.getRelease(ctx, "alice-tool", "1.0.0")
	add("getRelease", err)

	_, _, err = s.publishPackage(ctx, publisher, NewPackage{Platform: "lumen", Name: "alice-tool"})
	add("publishPackage", err)

	_, err = s.publishRelease(ctx, publisher, packaged, NewRelease{Version: "1.0.0"})
	add("publishRelease", err)

	add("deletePackage", s.deletePackage(ctx, publisher, packaged))

	_, err = s.getReadme(ctx, uuid.New())
	add("getReadme", err)

	add("saveReadme", s.saveReadme(ctx, readmeCache{ReleaseID: uuid.New(), Status: statusMissing}))

	add("saveDownloadSnapshot", s.saveDownloadSnapshot(ctx, uuid.New(), time.Now(), 1))

	_, err = s.packageDownloads(ctx, uuid.New())
	add("packageDownloads", err)

	_, err = s.collectTargets(ctx)
	add("collectTargets", err)

	_, err = s.readmeFor(ctx, Release{ID: uuid.New()}, false)
	add("readmeFor", err)

	add("Collect", Collect(ctx, discardLogger(), s.db))

	for _, c := range calls {
		if c.err == nil {
			t.Errorf("%s returned nil, want a query error", c.name)
		}
	}

	// A failed lookup is not mistaken for a missing row.
	if _, err := s.getUser(ctx, "alice"); errors.Is(err, ErrUserNotFound) {
		t.Error("a query failure was reported as ErrUserNotFound")
	}
	if _, err := s.getPackage(ctx, "alice-tool"); errors.Is(err, ErrPackageNotFound) {
		t.Error("a query failure was reported as ErrPackageNotFound")
	}
	if _, err := s.sessionUser(ctx, "deadbeef"); errors.Is(err, ErrInvalidCredentials) {
		t.Error("a query failure was reported as ErrInvalidCredentials")
	}
	if _, err := s.tokenUser(ctx, "lpm_deadbeef"); errors.Is(err, ErrInvalidCredentials) {
		t.Error("a query failure was reported as ErrInvalidCredentials")
	}
	if _, err := s.getReadme(ctx, uuid.New()); errors.Is(err, ErrReadmeNotCached) {
		t.Error("a query failure was reported as ErrReadmeNotCached")
	}
}

// TestAttachReleasesWithNoPackages covers the early return before any query.
func TestAttachReleasesWithNoPackages(t *testing.T) {
	s := NewServer(deadPool(t))

	if err := s.attachReleases(context.Background(), nil); err != nil {
		t.Errorf("attachReleases(nil) = %v, want nil", err)
	}
}

// TestPublishReleaseRejectsAnotherPublisher covers the ownership checks that
// run before any query.
func TestPublishReleaseRejectsAnotherPublisher(t *testing.T) {
	s := NewServer(deadPool(t))

	publisher := User{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111")}
	packaged := Package{PublisherID: uuid.MustParse("22222222-2222-2222-2222-222222222222")}

	_, err := s.publishRelease(context.Background(), publisher, packaged,
		NewRelease{Version: "1.0.0"})

	if !errors.Is(err, ErrNotPublisher) {
		t.Errorf("err = %v, want ErrNotPublisher", err)
	}

	if err := s.deletePackage(context.Background(), publisher, packaged); !errors.Is(err, ErrNotPublisher) {
		t.Errorf("delete err = %v, want ErrNotPublisher", err)
	}
}

func TestRecovererAnswersAPanic(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := Chain(panicking, RequestLogger(discardLogger()), Recoverer())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body = %q, want the JSON error", rec.Body)
	}
}

func TestRecovererLeavesAStartedResponseAlone(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, StatusResponse{Status: "partial"})
		panic("boom")
	})
	// RequestLogger wraps the writer, so Recoverer can see the response began.
	handler := Chain(panicking, RequestLogger(discardLogger()), Recoverer())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the 200 the handler already sent", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body = %q, want no error appended", rec.Body)
	}
}

func TestRecovererStaysQuietWhenTheClientIsGone(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := Chain(panicking, Recoverer())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written for a gone client", rec.Body)
	}
}

func TestResponseRecorderTracksTheResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	recorder := &responseRecorder{ResponseWriter: rec, status: http.StatusOK}

	if recorder.WroteHeader() {
		t.Error("WroteHeader is true before anything was written")
	}
	if recorder.Unwrap() != http.ResponseWriter(rec) {
		t.Error("Unwrap did not return the wrapped writer")
	}

	n, err := recorder.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !recorder.WroteHeader() || recorder.status != http.StatusOK {
		t.Errorf("status = %d, wroteHeader = %v, want 200 and true", recorder.status, recorder.WroteHeader())
	}
	if recorder.bytes != 5 {
		t.Errorf("bytes = %d, want 5", recorder.bytes)
	}

	// A second WriteHeader is ignored rather than passed through.
	recorder.WriteHeader(http.StatusTeapot)
	if recorder.status != http.StatusOK {
		t.Errorf("status = %d, want the first one to stand", recorder.status)
	}
}

func TestWriteJSONFallsBackWhenEncodingFails(t *testing.T) {
	rec := httptest.NewRecorder()

	// A channel cannot be marshalled.
	writeJSON(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, make(chan int))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != `{"error":"internal server error"}` {
		t.Errorf("body = %q, want the literal error", rec.Body)
	}
}

func TestWriteServerErrorMapsTheCause(t *testing.T) {
	deadline := httptest.NewRecorder()
	writeServerError(deadline, httptest.NewRequest(http.MethodGet, "/", nil),
		"query", context.DeadlineExceeded)
	if deadline.Code != http.StatusServiceUnavailable {
		t.Errorf("deadline status = %d, want 503", deadline.Code)
	}

	generic := httptest.NewRecorder()
	writeServerError(generic, httptest.NewRequest(http.MethodGet, "/", nil),
		"query", errors.New("boom"))
	if generic.Code != http.StatusInternalServerError {
		t.Errorf("generic status = %d, want 500", generic.Code)
	}

	// A client that hung up gets nothing at all.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gone := httptest.NewRecorder()
	writeServerError(gone, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx),
		"query", errors.New("boom"))
	if gone.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written", gone.Body)
	}
}

func TestWriteJSONSkipsBodyOnNotModified(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, httptest.NewRequest(http.MethodGet, "/", nil),
		http.StatusNotModified, StatusResponse{Status: "ignored"})

	if rec.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing", rec.Body)
	}
}

func TestTimeoutCancelsTheRequestContext(t *testing.T) {
	var deadline time.Time
	var ok bool

	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok = r.Context().Deadline()
	}), Timeout(time.Second))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !ok {
		t.Fatal("the handler saw no deadline")
	}
	if until := time.Until(deadline); until <= 0 || until > time.Second {
		t.Errorf("deadline in %v, want within a second", until)
	}
}

func TestLoggerFromFallsBackToTheDefault(t *testing.T) {
	if LoggerFrom(context.Background()) == nil {
		t.Error("LoggerFrom returned nil for a bare context")
	}

	logger := discardLogger()
	ctx := ContextWithLogger(context.Background(), logger)
	if LoggerFrom(ctx) != logger {
		t.Error("LoggerFrom did not return the stored logger")
	}
}

func TestRequestIDPrefersTheHeader(t *testing.T) {
	withHeader := httptest.NewRequest(http.MethodGet, "/", nil)
	withHeader.Header.Set("X-Request-Id", "given")
	if got := requestID(withHeader); got != "given" {
		t.Errorf("requestID = %q, want the header value", got)
	}

	generated := requestID(httptest.NewRequest(http.MethodGet, "/", nil))
	if len(generated) != 16 {
		t.Errorf("generated id = %q, want 16 hex characters", generated)
	}
	if generated == requestID(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Error("two generated ids matched")
	}
}

func TestValidationLengthLimits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }

	token := NewToken{Name: long(tokenNameMaxLen + 1)}
	if _, ok := token.Validate()["name"]; !ok {
		t.Error("NewToken.Validate did not reject a long name")
	}
	if _, ok := (&NewToken{Name: "  "}).Validate()["name"]; !ok {
		t.Error("NewToken.Validate did not reject a blank name")
	}

	pkg := NewPackage{
		Platform:    long(platformMaxLen + 1),
		Name:        long(packageNameMaxLen + 1),
		Description: long(descriptionMaxLen + 1),
	}
	for _, field := range []string{"platform", "name", "description"} {
		if _, ok := pkg.Validate()[field]; !ok {
			t.Errorf("NewPackage.Validate did not reject a long %s", field)
		}
	}

	rel := NewRelease{
		Version:      long(versionMaxLen + 1),
		Description:  long(descriptionMaxLen + 1),
		Dependencies: Requirements{"geom": long(requirementMaxLen + 1)},
		Requires:     Requirements{long(packageNameMaxLen + 1): "^1"},
		Artifacts: []NewArtifact{{
			Target: "any",
			URL:    "https://example.test/" + long(releaseURLMaxLen),
			SHA256: long(64),
			Size:   4,
		}},
	}
	relFields := rel.Validate()
	for _, field := range []string{
		"version",
		"description",
		"artifacts[0].url",
		"dependencies.geom",
		"requires." + long(packageNameMaxLen+1),
	} {
		if _, ok := relFields[field]; !ok {
			t.Errorf("NewRelease.Validate did not reject a long %s", field)
		}
	}

	// An unparseable URL is a field error, not a panic.
	broken := NewRelease{
		Version:   "1.0.0",
		Artifacts: []NewArtifact{{Target: "any", URL: "https://example.test/%zz", SHA256: long(64), Size: 4}},
	}
	if _, ok := broken.Validate()["artifacts[0].url"]; !ok {
		t.Error("NewRelease.Validate accepted an unparseable url")
	}

	// A requirement that does not parse names its own field, and the bare
	// `1.2` a package author writes is a caret requirement, not a range that
	// stops at 1.3.
	syntax := NewRelease{
		Version:      "1.0.0",
		Dependencies: Requirements{"geom": "newest please", "shapes": " 1.2 "},
		Requires:     Requirements{"lumenc": ">="},
		Artifacts:    []NewArtifact{{Target: "any", URL: "https://example.test/x.tgz", SHA256: long(64), Size: 4}},
	}
	syntaxFields := syntax.Validate()
	for _, field := range []string{"dependencies.geom", "requires.lumenc"} {
		if _, ok := syntaxFields[field]; !ok {
			t.Errorf("NewRelease.Validate accepted an unreadable %s", field)
		}
	}
	if _, ok := syntaxFields["dependencies.shapes"]; ok {
		t.Errorf("NewRelease.Validate rejected a bare requirement: %v", syntaxFields)
	}
	if syntax.Dependencies["shapes"] != "1.2" {
		t.Errorf("dependencies = %v, want the requirement trimmed and stored as written", syntax.Dependencies)
	}

	// Over a cap is one error on the map or the list, not one per entry.
	crowded := NewRelease{Version: "1.0.0", Dependencies: Requirements{}}
	for i := range requirementsMax + 1 {
		crowded.Dependencies[fmt.Sprintf("dep%d", i)] = "^1"
	}
	crowded.Artifacts = make([]NewArtifact, artifactsMax+1)
	crowdedFields := crowded.Validate()
	for _, field := range []string{"dependencies", "artifacts"} {
		if _, ok := crowdedFields[field]; !ok {
			t.Errorf("NewRelease.Validate accepted more %s than the cap", field)
		}
	}

	filter := PackageFilter{Platform: long(filterValueMaxLen + 1), Version: long(filterValueMaxLen + 1)}
	if fields := filter.Validate(); len(fields) != 2 {
		t.Errorf("PackageFilter.Validate = %v, want platform and version rejected", fields)
	}
}
