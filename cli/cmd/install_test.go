package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lumen-fx/registry/cli/internal"
)

// runLPM drives the root command with the flag state reset, because cobra
// keeps flag values between runs and a test is a second run.
func runLPM(t *testing.T, args ...string) (string, error) {
	t.Helper()

	installOpts = installOptions{}
	updateOpts = installOptions{}
	searchPlatform, searchRegistry, infoRegistry = "", "", ""

	return run(t, "", args...)
}

func archiveOf(t *testing.T, name, body string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Of(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// registryFixture is a registry with two packages and a host for their
// archives, all on one test server.
type registryFixture struct {
	URL string
	// The digest each archive path hashes to, for asserting on the lock.
	digests map[string]string
	// Set to serve a body that does not match the published digest.
	tamper bool
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()

	fixture := &registryFixture{digests: map[string]string{}}

	archives := map[string][]byte{
		"/a/shape-tools-1.2.3.tar.gz": archiveOf(t, "libshape_tools.so", "shape code"),
		"/a/shape-tools-1.9.0.tar.gz": archiveOf(t, "libshape_tools.so", "newer shape code"),
		"/a/geom-0.3.1.tar.gz":        archiveOf(t, "libgeom.so", "geom code"),
	}

	mux := http.NewServeMux()
	for path, body := range archives {
		fixture.digests[path] = sha256Of(body)
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			if fixture.tamper {
				w.Write([]byte("something else entirely"))
				return
			}
			w.Write(body)
		})
	}

	release := func(version, path string, dependencies, requires string) string {
		return fmt.Sprintf(
			`{"id":"11111111-1111-1111-1111-111111111111","version":%q,"dependencies":%s,"requires":%s,`+
				`"artifacts":[{"target":"any","url":"%s%s","sha256":%q,"size":%d}]}`,
			version, dependencies, requires, fixture.URL, path,
			fixture.digests[path], len(archives[path]))
	}

	mux.HandleFunc("GET /packages/shape-tools", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"22222222-2222-2222-2222-222222222222","platform":"lumen","name":"shape-tools",`+
			`"description":"shapes","releases":[%s,%s]}`,
			release("1.9.0", "/a/shape-tools-1.9.0.tar.gz", `{"geom":"^0.3"}`, `{"lumenc":">=0.2"}`),
			release("1.2.3", "/a/shape-tools-1.2.3.tar.gz", `{"geom":"^0.3"}`, `{"lumenc":">=0.2"}`))
	})
	mux.HandleFunc("GET /packages/geom", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"33333333-3333-3333-3333-333333333333","platform":"lumen","name":"geom",`+
			`"description":"geometry","releases":[%s]}`,
			release("0.3.1", "/a/geom-0.3.1.tar.gz", `{}`, `{}`))
	})
	mux.HandleFunc("GET /packages/absent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"package doesn't exist"}`)
	})
	mux.HandleFunc("GET /packages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "nothing" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, `[{"id":"22222222-2222-2222-2222-222222222222","platform":"lumen","name":"shape-tools",`+
			`"description":"shapes","releases":[{"version":"1.9.0","artifacts":[]}]},`+
			`{"id":"44444444-4444-4444-4444-444444444444","platform":"candela","name":"quiet",`+
			`"description":"nothing yet","releases":[]}]`)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fixture.URL = server.URL
	return fixture
}

// installEnv points the config, the cache, and the lock at temporary
// directories, so a test never touches the machine's own.
func installEnv(t *testing.T) (fixture *registryFixture, lockPath string) {
	t.Helper()

	t.Setenv("LPM_CONFIG_DIR", t.TempDir())
	t.Setenv("LPM_CACHE_DIR", t.TempDir())
	t.Setenv("LPM_REGISTRY", "")

	return newRegistryFixture(t), filepath.Join(t.TempDir(), "lumen.lock")
}

func TestInstallResolvesDownloadsAndLocks(t *testing.T) {
	fixture, lockPath := installEnv(t)

	out, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2", "--json")
	if err != nil {
		t.Fatalf("install: %v (%s)", err, out)
	}

	var report internal.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if report.Schema != internal.ReportSchema {
		t.Errorf("schema = %d, want %d", report.Schema, internal.ReportSchema)
	}
	if report.Lock != lockPath {
		t.Errorf("lock = %q, want %q", report.Lock, lockPath)
	}
	if len(report.Packages) != 2 {
		t.Fatalf("packages = %+v, want shape-tools and geom", report.Packages)
	}

	byName := map[string]internal.InstalledPackage{}
	for _, p := range report.Packages {
		byName[p.Name] = p
	}

	shapes := byName["shape-tools"]
	if shapes.Version != "1.9.0" || shapes.Platform != "lumen" || shapes.Target != "any" {
		t.Errorf("shape-tools = %+v, want 1.9.0 on the any target", shapes)
	}
	if len(shapes.Files) != 1 || shapes.Files[0] != "libshape_tools.so" {
		t.Errorf("files = %v, want the library alone, with no marker", shapes.Files)
	}
	if shapes.Dependencies["geom"] != "0.3.1" {
		t.Errorf("dependencies = %v, want geom 0.3.1", shapes.Dependencies)
	}

	// The cache holds the unpacked package where the report says it does.
	body, err := os.ReadFile(filepath.Join(shapes.Dir, "libshape_tools.so"))
	if err != nil || string(body) != "newer shape code" {
		t.Errorf("cached library = %q, %v", body, err)
	}

	// And the lock records both packages with the digests that were verified.
	lock, err := internal.ReadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Packages) != 2 {
		t.Fatalf("lock holds %d packages, want 2", len(lock.Packages))
	}
	pin, _ := lock.Find("shape-tools")
	want := "sha256:" + fixture.digests["/a/shape-tools-1.9.0.tar.gz"]
	if pin.Version != "1.9.0" || pin.Artifacts["any"] != want {
		t.Errorf("pin = %+v, want 1.9.0 with %s", pin, want)
	}
	if pin.Registry != fixture.URL || pin.Platform != "lumen" {
		t.Errorf("pin = %+v, want the registry and platform recorded", pin)
	}
	if len(pin.Dependencies) != 1 || pin.Dependencies[0] != "geom 0.3.1" {
		t.Errorf("pin dependencies = %v, want `geom 0.3.1`", pin.Dependencies)
	}
}

func TestInstallWithoutJSONPrintsOneLinePerPackage(t *testing.T) {
	fixture, lockPath := installEnv(t)

	out, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2")
	if err != nil {
		t.Fatalf("install: %v (%s)", err, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("out = %q, want one line per package", out)
	}
	for _, line := range lines {
		if !strings.Contains(line, "(any)") {
			t.Errorf("line = %q, want the target in it", line)
		}
	}
}

// A second install finds the lock already correct, so it writes nothing.
func TestInstallLeavesAnUpToDateLockAlone(t *testing.T) {
	fixture, lockPath := installEnv(t)

	args := []string{"install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2"}

	if _, err := runLPM(t, args...); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runLPM(t, args...); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("the lock changed on a second identical install:\n%s\n%s", first, second)
	}

	// --locked is happy with a lock that would not change.
	locked := append(append([]string{}, args...), "--locked")
	if _, err := runLPM(t, locked...); err != nil {
		t.Errorf("--locked on an up-to-date lock = %v, want success", err)
	}
}

func TestInstallLockedRefusesToWriteAStaleLock(t *testing.T) {
	fixture, lockPath := installEnv(t)

	_, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2", "--locked")
	if err == nil {
		t.Fatal("--locked wrote a lock that did not exist")
	}
	if got := internal.ExitCodeFor(err); got != internal.ExitLockChange {
		t.Errorf("exit code = %d, want %d", got, internal.ExitLockChange)
	}
	if _, statErr := os.Stat(lockPath); statErr == nil {
		t.Error("--locked wrote the lock file anyway")
	}
}

func TestInstallOfflineUsesTheLockAndTheCache(t *testing.T) {
	fixture, lockPath := installEnv(t)

	args := []string{"install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2"}
	if _, err := runLPM(t, args...); err != nil {
		t.Fatal(err)
	}

	// Everything is cached, so offline finds it without a registry.
	out, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64",
		"--req", "shape-tools@^1.2", "--offline", "--json")
	if err != nil {
		t.Fatalf("offline install: %v (%s)", err, out)
	}
	var report internal.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Packages) != 2 {
		t.Errorf("packages = %+v, want both from the cache", report.Packages)
	}
}

func TestInstallOfflineStopsAtAMiss(t *testing.T) {
	_, lockPath := installEnv(t)

	// Nothing has been installed, so the lock names nothing.
	_, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64",
		"--req", "shape-tools@^1.2", "--offline")
	if err == nil {
		t.Fatal("offline install succeeded with an empty lock")
	}
	if got := internal.ExitCodeFor(err); got != internal.ExitOffline {
		t.Errorf("exit code = %d, want %d", got, internal.ExitOffline)
	}

	// A pin the requirement has outgrown is a miss too: offline was not
	// allowed to look for the version that would satisfy it.
	fixtureForPin := newRegistryFixture(t)
	if _, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixtureForPin.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@=1.2.3"); err != nil {
		t.Fatal(err)
	}
	_, err = runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64",
		"--req", "shape-tools@^1.9", "--offline")
	if got := internal.ExitCodeFor(err); got != internal.ExitOffline {
		t.Errorf("exit code = %d (%v), want %d", got, err, internal.ExitOffline)
	}
	if err == nil || !strings.Contains(err.Error(), "--offline resolved from the lock alone") {
		t.Errorf("err = %v, want it to say the lock was all it read", err)
	}

	// And when the lock has the pin but the cache was cleared.
	fixture := newRegistryFixture(t)
	if _, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LPM_CACHE_DIR", t.TempDir())

	_, err = runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64",
		"--req", "shape-tools@^1.2", "--offline")
	if got := internal.ExitCodeFor(err); got != internal.ExitOffline {
		t.Errorf("exit code = %d (%v), want %d", got, err, internal.ExitOffline)
	}
}

func TestInstallRefusesAnArtifactThatDoesNotMatchItsDigest(t *testing.T) {
	fixture, lockPath := installEnv(t)
	fixture.tamper = true

	_, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2")
	if err == nil {
		t.Fatal("install accepted an artifact the host tampered with")
	}
	if !strings.Contains(err.Error(), "bytes, and the registry lists") &&
		!strings.Contains(err.Error(), "hashes to") {
		t.Errorf("err = %q, want a verification refusal", err)
	}

	// Nothing landed: no cache entry and no lock.
	cache, err := internal.OpenCache()
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(filepath.Join(cache.Root, "pkgs")); err == nil && len(entries) > 0 {
		t.Errorf("the cache holds %v after a refused download", entries)
	}
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("a refused download still wrote the lock")
	}
}

func TestUpdateMovesOnlyTheNamedPin(t *testing.T) {
	fixture, lockPath := installEnv(t)

	// Pin shape-tools at 1.2.3 by asking for exactly that.
	if _, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@=1.2.3"); err != nil {
		t.Fatal(err)
	}
	lock, err := internal.ReadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if pin, _ := lock.Find("shape-tools"); pin.Version != "1.2.3" {
		t.Fatalf("pin = %s, want 1.2.3", pin.Version)
	}

	// A wider requirement keeps the pin.
	if _, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2"); err != nil {
		t.Fatal(err)
	}
	lock, _ = internal.ReadLock(lockPath)
	if pin, _ := lock.Find("shape-tools"); pin.Version != "1.2.3" {
		t.Errorf("pin = %s, want the install to leave 1.2.3 alone", pin.Version)
	}

	// update moves it.
	if _, err := runLPM(t, "update", "shape-tools",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2"); err != nil {
		t.Fatal(err)
	}
	lock, _ = internal.ReadLock(lockPath)
	if pin, _ := lock.Find("shape-tools"); pin.Version != "1.9.0" {
		t.Errorf("pin = %s, want update to move it to 1.9.0", pin.Version)
	}
}

func TestUpdateWithNoNamesMovesEverything(t *testing.T) {
	fixture, lockPath := installEnv(t)

	if _, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@=1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := runLPM(t, "update",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.4.0", "--req", "shape-tools@^1.2"); err != nil {
		t.Fatal(err)
	}

	lock, err := internal.ReadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if pin, _ := lock.Find("shape-tools"); pin.Version != "1.9.0" {
		t.Errorf("pin = %s, want 1.9.0", pin.Version)
	}
}

func TestInstallReportsUsageMistakes(t *testing.T) {
	fixture, lockPath := installEnv(t)

	for name, args := range map[string][]string{
		"no lock":        {"install", "--target", "linux-x86_64"},
		"no target":      {"install", "--lock", lockPath},
		"any as target":  {"install", "--lock", lockPath, "--target", "any"},
		"unknown target": {"install", "--lock", lockPath, "--target", "linux-amd64"},
		"bad req":        {"install", "--lock", lockPath, "--target", "linux-x86_64", "--req", "shape-tools"},
		"bad host":       {"install", "--lock", lockPath, "--target", "linux-x86_64", "--host", "lumenc"},
		"offline and registry": {"install", "--lock", lockPath, "--target", "linux-x86_64",
			"--offline", "--registry", fixture.URL},
		"unknown flag": {"install", "--lock", lockPath, "--target", "linux-x86_64", "--nope"},
	} {
		_, err := runLPM(t, args...)
		if err == nil {
			t.Errorf("%s: succeeded, want a usage error", name)
			continue
		}
		if got := internal.ExitCodeFor(err); got != internal.ExitUsage {
			t.Errorf("%s: exit code = %d (%v), want %d", name, got, err, internal.ExitUsage)
		}
	}
}

func TestInstallReportsAnUnresolvableRequirement(t *testing.T) {
	fixture, lockPath := installEnv(t)

	// The host is too old for anything shape-tools published.
	_, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--host", "lumenc@0.1.0", "--req", "shape-tools@^1.2")
	if err == nil || !strings.Contains(err.Error(), "lumenc") {
		t.Errorf("err = %v, want it to name the host", err)
	}
	if got := internal.ExitCodeFor(err); got != internal.ExitError {
		t.Errorf("exit code = %d, want %d", got, internal.ExitError)
	}

	_, err = runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--req", "absent@^1")
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Errorf("err = %v, want it to name the missing package", err)
	}
}

func TestInstallRejectsALockFromAnotherVersion(t *testing.T) {
	fixture, lockPath := installEnv(t)
	if err := os.WriteFile(lockPath, []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL,
		"--req", "shape-tools@^1.2")
	if err == nil || !strings.Contains(err.Error(), "version 1") {
		t.Errorf("err = %v, want it to name the lock version", err)
	}
}

// Nothing asked for is nothing installed, and the report says so.
func TestInstallWithNoRequirements(t *testing.T) {
	fixture, lockPath := installEnv(t)

	out, err := runLPM(t, "install",
		"--lock", lockPath, "--target", "linux-x86_64", "--registry", fixture.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}

	var report internal.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Packages == nil || len(report.Packages) != 0 {
		t.Errorf("packages = %v, want an empty list rather than null", report.Packages)
	}
}

func TestSearchListsMatches(t *testing.T) {
	fixture, _ := installEnv(t)

	out, err := runLPM(t, "search", "shape", "--registry", fixture.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shape-tools", "lumen", "1.9.0", "quiet", "no releases"} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want it to mention %q", out, want)
		}
	}

	out, err = runLPM(t, "search", "nothing", "--registry", fixture.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing matches") {
		t.Errorf("out = %q, want the empty-result line", out)
	}

	if _, err := runLPM(t, "search"); err == nil ||
		internal.ExitCodeFor(err) != internal.ExitUsage {
		t.Errorf("search with no query = %v, want a usage error", err)
	}
}

func TestInfoShowsReleasesAndRequirements(t *testing.T) {
	fixture, _ := installEnv(t)

	out, err := runLPM(t, "info", "shape-tools", "--registry", fixture.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shape-tools (lumen)", "shapes", "1.9.0", "any",
		"depends on geom ^0.3", "needs lumenc >=0.2"} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want it to mention %q", out, want)
		}
	}

	if _, err := runLPM(t, "info", "absent", "--registry", fixture.URL); err == nil {
		t.Error("info succeeded on a package that does not exist")
	}
}
