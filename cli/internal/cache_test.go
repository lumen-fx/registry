package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCache(t *testing.T) *Cache {
	t.Helper()
	t.Setenv("LPM_CACHE_DIR", t.TempDir())

	cache, err := OpenCache()
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestOpenCacheHonoursTheOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LPM_CACHE_DIR", root)

	cache, err := OpenCache()
	if err != nil {
		t.Fatal(err)
	}
	if cache.Root != root {
		t.Errorf("root = %q, want %q", cache.Root, root)
	}

	dir := cache.Dir("shape-tools", "1.2.3", "linux-x86_64")
	want := filepath.Join(root, "pkgs", "shape-tools", "1.2.3", "linux-x86_64")
	if dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}
}

// The --json report gives absolute paths, whatever LPM_CACHE_DIR held.
func TestOpenCacheReturnsAnAbsoluteRoot(t *testing.T) {
	t.Setenv("LPM_CACHE_DIR", "relative-cache")

	cache, err := OpenCache()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cache.Root) {
		t.Errorf("root = %q, want an absolute path", cache.Root)
	}
}

func TestOpenCacheFallsBackToTheUserDir(t *testing.T) {
	t.Setenv("LPM_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cache, err := OpenCache()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cache.Root) != "lpm" {
		t.Errorf("root = %q, want it under a directory named lpm", cache.Root)
	}
}

func TestStoreUnpacksAndMarks(t *testing.T) {
	cache := testCache(t)
	archive := tarGz(t, entry{name: "libshape_tools.so", body: "code", mode: 0o755})
	digest := digestOf(archive)
	dir := cache.Dir("shape-tools", "1.2.3", "linux-x86_64")

	if cache.Has(dir, digest) {
		t.Fatal("an empty cache reported a hit")
	}
	if err := cache.Store(dir, digest, archive, "https://example.test/a.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if !cache.Has(dir, digest) {
		t.Error("a stored package did not report a hit")
	}
	if cache.Has(dir, strings.Repeat("b", 64)) {
		t.Error("a stored package matched another digest")
	}

	files, err := cache.Files(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "libshape_tools.so" {
		t.Errorf("files = %v, want the library alone, without the marker", files)
	}
}

// A directory an interrupted run left behind carries no marker, so it is
// replaced rather than trusted.
func TestStoreReplacesAnUnmarkedDirectory(t *testing.T) {
	cache := testCache(t)
	dir := cache.Dir("shape-tools", "1.2.3", AnyTarget)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leftover"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cache.Has(dir, digestOf(nil)) {
		t.Fatal("a directory with no marker reported a hit")
	}

	archive := tarGz(t, entry{name: "shapes.cdl", body: "fn main() {}"})
	if err := cache.Store(dir, digestOf(archive), archive, "a.tar.gz"); err != nil {
		t.Fatal(err)
	}

	files, err := cache.Files(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "shapes.cdl" {
		t.Errorf("files = %v, want the leftover gone", files)
	}
}

// A failed unpack leaves nothing a later run would mistake for a hit.
func TestStoreLeavesNothingBehindWhenUnpackFails(t *testing.T) {
	cache := testCache(t)
	dir := cache.Dir("shape-tools", "1.2.3", AnyTarget)

	err := cache.Store(dir, digestOf(nil), []byte("not an archive"), "a.tar.gz")
	if err == nil {
		t.Fatal("Store accepted something that is not an archive")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a failed Store left a package directory behind")
	}

	// And the staging directory it unpacked into is gone too.
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%v was left in the cache", entries)
	}
}

func TestStoreReportsAParentItCannotCreate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LPM_CACHE_DIR", filepath.Join(root, "cache"))
	if err := os.WriteFile(filepath.Join(root, "cache"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache, err := OpenCache()
	if err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, entry{name: "lib.so", body: "code"})
	dir := cache.Dir("shape-tools", "1.2.3", AnyTarget)

	if err := cache.Store(dir, digestOf(archive), archive, "a.tar.gz"); err == nil {
		t.Error("Store succeeded under a path that is a file")
	}
}

func TestFilesReportsAMissingDirectory(t *testing.T) {
	cache := testCache(t)
	if _, err := cache.Files(cache.Dir("absent", "1.0.0", AnyTarget)); err == nil {
		t.Error("Files succeeded on a directory that does not exist")
	}
}
