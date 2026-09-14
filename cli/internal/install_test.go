package internal

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func choiceFor(t *testing.T, name, version, target string, archive []byte, url string) Choice {
	t.Helper()
	return Choice{
		Name:     name,
		Version:  version,
		Platform: "lumen",
		Artifact: Artifact{
			Target: target, URL: url, SHA256: digestOf(archive), Size: int64(len(archive)),
		},
		Specs:        Requirements{},
		Dependencies: map[string]string{},
	}
}

func TestInstallDownloadsVerifiesAndReuses(t *testing.T) {
	archive := tarGz(t, entry{name: "libshape_tools.so", body: "code", mode: 0o755})
	host := artifactHost(t, archive)
	cache := testCache(t)

	fetcher, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}
	choice := choiceFor(t, "shape-tools", "1.2.3", "linux-x86_64", archive, host.URL+"/archive")

	installed, err := Install(cache, fetcher, false, []Choice{choice})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 {
		t.Fatalf("installed = %+v, want one package", installed)
	}

	got := installed[0]
	want := cache.Dir("shape-tools", "1.2.3", "linux-x86_64")
	if got.Dir != want || !filepath.IsAbs(got.Dir) {
		t.Errorf("dir = %q, want the absolute cache path %q", got.Dir, want)
	}
	if len(got.Files) != 1 || got.Files[0] != "libshape_tools.so" {
		t.Errorf("files = %v, want the library alone", got.Files)
	}
	if got.Target != "linux-x86_64" || got.Platform != "lumen" {
		t.Errorf("installed = %+v, want the target and platform carried through", got)
	}

	// A second run finds it cached, so an unreachable URL does not matter.
	cached := choice
	cached.Artifact.URL = "http://127.0.0.1:1/gone"
	if _, err := Install(cache, fetcher, false, []Choice{cached}); err != nil {
		t.Errorf("second install = %v, want the cached copy reused", err)
	}
}

func TestInstallRefusesAWrongDigestAndCachesNothing(t *testing.T) {
	archive := tarGz(t, entry{name: "lib.so", body: "code"})
	host := artifactHost(t, archive)
	cache := testCache(t)

	fetcher, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}

	choice := choiceFor(t, "shape-tools", "1.2.3", AnyTarget, archive, host.URL+"/archive")
	choice.Artifact.SHA256 = strings.Repeat("b", 64)

	if _, err := Install(cache, fetcher, false, []Choice{choice}); err == nil ||
		!strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("Install = %v, want a digest refusal", err)
	}

	dir := cache.Dir("shape-tools", "1.2.3", AnyTarget)
	if _, err := cache.Files(dir); err == nil {
		t.Error("a refused artifact left something in the cache")
	}
}

func TestInstallOfflineStopsAtACacheMiss(t *testing.T) {
	archive := tarGz(t, entry{name: "lib.so", body: "code"})
	cache := testCache(t)

	fetcher, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}
	choice := choiceFor(t, "shape-tools", "1.2.3", AnyTarget, archive, "https://example.test/a.tar.gz")

	_, err = Install(cache, fetcher, true, []Choice{choice})
	if err == nil {
		t.Fatal("offline install succeeded with an empty cache")
	}
	if ExitCodeFor(err) != ExitOffline {
		t.Errorf("exit code = %d, want %d", ExitCodeFor(err), ExitOffline)
	}
	if !strings.Contains(err.Error(), "not in the cache") {
		t.Errorf("err = %q, want it to say the cache has nothing", err)
	}
}

func TestLockForRecordsWhatWasResolved(t *testing.T) {
	choices := []Choice{
		{
			Name: "shape-tools", Version: "1.2.3", Platform: "lumen",
			Artifact:     Artifact{Target: "linux-x86_64", SHA256: "0123"},
			Dependencies: map[string]string{"geom": "0.3.1"},
		},
		{
			Name: "geom", Version: "0.3.1", Platform: "lumen",
			Artifact: Artifact{Target: AnyTarget, SHA256: "4567"},
		},
	}

	lock := LockFor(&Lock{Version: LockVersion}, "https://reg.lumenfx.dev", choices)
	if lock.Version != LockVersion || len(lock.Packages) != 2 {
		t.Fatalf("lock = %+v, want two packages at version %d", lock, LockVersion)
	}
	if lock.Packages[0].Name != "geom" {
		t.Errorf("packages = %+v, want them sorted by name", lock.Packages)
	}

	pin, _ := lock.Find("shape-tools")
	if pin.Registry != "https://reg.lumenfx.dev" {
		t.Errorf("registry = %q, want the one resolved against", pin.Registry)
	}
	if len(pin.Dependencies) != 1 || pin.Dependencies[0] != "geom 0.3.1" {
		t.Errorf("dependencies = %v, want `geom 0.3.1`", pin.Dependencies)
	}
	if pin.Artifacts["linux-x86_64"] != "sha256:0123" {
		t.Errorf("artifacts = %v, want the digest with its prefix", pin.Artifacts)
	}
}

// A machine installing for one target keeps what another machine verified for
// the same version, and drops it when the version moves.
func TestLockForKeepsOtherTargetsAtTheSameVersion(t *testing.T) {
	previous := &Lock{Version: LockVersion, Packages: []LockedPackage{{
		Name: "shape-tools", Version: "1.2.3",
		Artifacts: map[string]string{"macos-aarch64": "sha256:mac"},
	}}}

	same := LockFor(previous, "https://reg.lumenfx.dev", []Choice{{
		Name: "shape-tools", Version: "1.2.3", Platform: "lumen",
		Artifact: Artifact{Target: "linux-x86_64", SHA256: "linux"},
	}})
	pin, _ := same.Find("shape-tools")
	if len(pin.Artifacts) != 2 || pin.Artifacts["macos-aarch64"] != "sha256:mac" {
		t.Errorf("artifacts = %v, want both targets", pin.Artifacts)
	}

	moved := LockFor(previous, "https://reg.lumenfx.dev", []Choice{{
		Name: "shape-tools", Version: "1.3.0", Platform: "lumen",
		Artifact: Artifact{Target: "linux-x86_64", SHA256: "linux"},
	}})
	pin, _ = moved.Find("shape-tools")
	if len(pin.Artifacts) != 1 || pin.Artifacts["linux-x86_64"] != "sha256:linux" {
		t.Errorf("artifacts = %v, want only what this version verified", pin.Artifacts)
	}
}

func TestExitCodeForReadsTheCode(t *testing.T) {
	if got := ExitCodeFor(nil); got != ExitOK {
		t.Errorf("ExitCodeFor(nil) = %d, want %d", got, ExitOK)
	}
	if got := ExitCodeFor(Fail(ExitUsage, "bad flag")); got != ExitUsage {
		t.Errorf("ExitCodeFor = %d, want %d", got, ExitUsage)
	}

	// Wrapped, which is how a coded failure travels back up through a call.
	wrapped := Fail(ExitLockChange, "stale")
	if got := ExitCodeFor(wrapError(wrapped)); got != ExitLockChange {
		t.Errorf("ExitCodeFor(wrapped) = %d, want %d", got, ExitLockChange)
	}

	if got := ExitCodeFor(errPlain{}); got != ExitError {
		t.Errorf("ExitCodeFor(plain) = %d, want %d", got, ExitError)
	}
	if msg := Fail(ExitUsage, "bad %s", "flag").Error(); msg != "bad flag" {
		t.Errorf("Error() = %q, want the formatted message", msg)
	}
}

func TestCodedErrorUnwrapsToItsCause(t *testing.T) {
	cause := errPlain{}
	coded := &CodedError{Code: ExitOffline, Err: cause}

	if !errors.Is(coded, cause) {
		t.Error("a coded error does not unwrap to its cause")
	}
	if coded.Unwrap() != error(cause) {
		t.Errorf("Unwrap() = %v, want the cause", coded.Unwrap())
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "plain" }

func wrapError(err error) error {
	return &wrapper{err}
}

type wrapper struct{ err error }

func (w *wrapper) Error() string { return "context: " + w.err.Error() }
func (w *wrapper) Unwrap() error { return w.err }
