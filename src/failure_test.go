package src

import (
	"context"
	"errors"
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

	const credentials = "alice:correct-horse-battery"

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
		{http.MethodPost, "/user/register", `{"username":"alice","email":"a@example.test","password":"correct-horse-battery"}`, false, http.StatusInternalServerError},
		{http.MethodPost, "/user/login", `{"username":"alice","password":"correct-horse-battery"}`, false, http.StatusInternalServerError},
		{http.MethodPost, "/user/change_password", `{"username":"alice","currentPassword":"correct-horse-battery","newPassword":"another-good-one"}`, false, http.StatusInternalServerError},
		{http.MethodPost, "/packages", `{"platform":"linux","name":"alice-tool"}`, true, http.StatusInternalServerError},
		{http.MethodPost, "/packages/alice-tool/releases", `{"url":"https://example.test/x.tgz","version":"1.0.0"}`, true, http.StatusInternalServerError},
	} {
		var body *strings.Reader
		if c.body == "" {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(c.body)
		}

		req := httptest.NewRequest(c.method, c.path, body)
		if c.auth {
			user, password, _ := strings.Cut(credentials, ":")
			req.SetBasicAuth(user, password)
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

	_, err := s.createUser(ctx, UserRegister{Username: "alice", Email: "a@example.test", Password: "correct-horse-battery"})
	add("createUser", err)

	_, err = s.getUser(ctx, "alice")
	add("getUser", err)

	_, err = s.verifyLogin(ctx, UserLogin{Username: "alice", Password: "correct-horse-battery"})
	add("verifyLogin", err)

	add("changePassword", s.changePassword(ctx, UserResetPassword{
		Username: "alice", CurrentPassword: "correct-horse-battery", NewPassword: "another-good-one",
	}))

	_, err = s.listPackages(ctx, PackageFilter{})
	add("listPackages", err)

	_, err = s.getPackage(ctx, "alice-tool")
	add("getPackage", err)

	_, err = s.getRelease(ctx, "alice-tool", "1.0.0")
	add("getRelease", err)

	_, err = s.publishPackage(ctx, publisher, NewPackage{Platform: "linux", Name: "alice-tool"})
	add("publishPackage", err)

	_, err = s.publishRelease(ctx, publisher, packaged, NewRelease{URL: "https://example.test/x.tgz", Version: "1.0.0"})
	add("publishRelease", err)

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
}

// TestAttachReleasesWithNoPackages covers the early return before any query.
func TestAttachReleasesWithNoPackages(t *testing.T) {
	s := NewServer(deadPool(t))

	if err := s.attachReleases(context.Background(), nil); err != nil {
		t.Errorf("attachReleases(nil) = %v, want nil", err)
	}
}

// TestPublishReleaseRejectsAnotherPublisher covers the ownership check before
// any query runs.
func TestPublishReleaseRejectsAnotherPublisher(t *testing.T) {
	s := NewServer(deadPool(t))

	publisher := User{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111")}
	packaged := Package{PublisherID: uuid.MustParse("22222222-2222-2222-2222-222222222222")}

	_, err := s.publishRelease(context.Background(), publisher, packaged,
		NewRelease{URL: "https://example.test/x.tgz", Version: "1.0.0"})

	if !errors.Is(err, ErrNotPublisher) {
		t.Errorf("err = %v, want ErrNotPublisher", err)
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

	register := UserRegister{
		Username: long(usernameMaxLen + 1),
		Email:    long(emailMaxLen) + "@example.test",
		Password: long(passwordMaxLen + 1),
	}
	for _, field := range []string{"username", "email", "password"} {
		if _, ok := register.Validate()[field]; !ok {
			t.Errorf("UserRegister.Validate did not reject a long %s", field)
		}
	}

	login := UserLogin{Username: long(usernameMaxLen + 1), Password: long(passwordMaxLen + 1)}
	if fields := login.Validate(); len(fields) != 2 {
		t.Errorf("UserLogin.Validate = %v, want both fields rejected", fields)
	}

	reset := UserResetPassword{Username: "alice", CurrentPassword: "current", NewPassword: ""}
	if _, ok := reset.Validate()["newPassword"]; !ok {
		t.Error("UserResetPassword.Validate did not reject an empty new password")
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
		URL:         "https://example.test/" + long(releaseURLMaxLen),
		Version:     long(versionMaxLen + 1),
		Description: long(descriptionMaxLen + 1),
	}
	for _, field := range []string{"url", "version", "description"} {
		if _, ok := rel.Validate()[field]; !ok {
			t.Errorf("NewRelease.Validate did not reject a long %s", field)
		}
	}

	// An unparseable URL is a field error, not a panic.
	broken := NewRelease{URL: "https://example.test/%zz", Version: "1.0.0"}
	if _, ok := broken.Validate()["url"]; !ok {
		t.Error("NewRelease.Validate accepted an unparseable url")
	}

	filter := PackageFilter{Platform: long(filterValueMaxLen + 1), Version: long(filterValueMaxLen + 1)}
	if fields := filter.Validate(); len(fields) != 2 {
		t.Errorf("PackageFilter.Validate = %v, want platform and version rejected", fields)
	}
}
