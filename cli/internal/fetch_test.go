package internal

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// entry is one member of a test archive.
type entry struct {
	name string
	body string
	mode int64
	link string // non-empty makes it a symlink
	dir  bool
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body))}
		if header.Mode == 0 {
			header.Mode = 0o644
		}
		switch {
		case e.link != "":
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, e.link, 0
		case e.dir:
			header.Typeflag, header.Size = tar.TypeDir, 0
		default:
			header.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, e := range entries {
		name := e.name
		if e.dir && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if e.mode != 0 {
			header.SetMode(os.FileMode(e.mode))
		}
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := writer.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// artifactHost serves a fixed body at /archive and records how the client
// asked for it.
func artifactHost(t *testing.T, body []byte) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /archive", func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	})
	mux.HandleFunc("GET /short", func(w http.ResponseWriter, r *http.Request) {
		w.Write(body[:len(body)/2])
	})
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /empty", func(w http.ResponseWriter, r *http.Request) {})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestDownloadAcceptsWhatMatches(t *testing.T) {
	body := tarGz(t, entry{name: "libshape_tools.so", body: "code", mode: 0o755})
	host := artifactHost(t, body)

	fetcher, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}

	got, err := fetcher.Download(Artifact{
		URL:    host.URL + "/archive",
		SHA256: digestOf(body),
		Size:   int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("Download returned bytes other than the ones served")
	}
}

func TestDownloadRefusesWhatDoesNotMatch(t *testing.T) {
	body := tarGz(t, entry{name: "lib.so", body: "code"})
	host := artifactHost(t, body)
	good := Artifact{URL: host.URL + "/archive", SHA256: digestOf(body), Size: int64(len(body))}

	fetcher, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}

	wrongDigest := good
	wrongDigest.SHA256 = strings.Repeat("b", 64)

	wrongSize := good
	wrongSize.Size = good.Size + 10

	tooBig := good
	tooBig.Size = maxArtifactBytes + 1

	noSize := good
	noSize.Size = 0

	short := good
	short.URL = host.URL + "/short"

	missing := good
	missing.URL = host.URL + "/missing"

	unreachable := good
	unreachable.URL = "http://127.0.0.1:1/archive"

	for name, c := range map[string]struct {
		artifact Artifact
		want     string
	}{
		"digest":       {wrongDigest, "hashes to"},
		"size":         {wrongSize, "bytes, and the registry lists"},
		"over the cap": {tooBig, "artifact size"},
		"no size":      {noSize, "artifact size"},
		"short body":   {short, "bytes, and the registry lists"},
		"404":          {missing, "answered 404"},
		"unreachable":  {unreachable, "download"},
	} {
		got, err := fetcher.Download(c.artifact)
		if err == nil {
			t.Errorf("%s: Download succeeded, want a refusal", name)
			continue
		}
		if got != nil {
			t.Errorf("%s: Download returned bytes alongside its error", name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %q, want it to mention %q", name, err, c.want)
		}
	}
}

func TestMeasureHashesWhatTheURLServes(t *testing.T) {
	body := tarGz(t, entry{name: "lib.so", body: "code"})
	host := artifactHost(t, body)

	fetcher, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}

	sum, size, err := fetcher.Measure(host.URL + "/archive")
	if err != nil {
		t.Fatal(err)
	}
	if sum != digestOf(body) || size != int64(len(body)) {
		t.Errorf("Measure = %s, %d, want %s, %d", sum, size, digestOf(body), len(body))
	}

	for _, c := range []struct{ path, want string }{
		{"/missing", "answered 404"},
		{"/empty", "is empty"},
	} {
		if _, _, err := fetcher.Measure(host.URL + c.path); err == nil ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("Measure(%s) = %v, want it to mention %q", c.path, err, c.want)
		}
	}
}

func TestNewFetcherReadsAnExtraCertificateAuthority(t *testing.T) {
	// A server whose certificate no system trusts, so the CA file is what
	// makes the connection work.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	t.Cleanup(server.Close)

	plain, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := plain.Measure(server.URL); err == nil {
		t.Fatal("the default fetcher trusted a certificate nothing signed")
	}

	pem := server.Certificate().Raw
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, encodePEM(pem), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LPM_CA_FILE", path)
	trusting, err := NewFetcher()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := trusting.Measure(server.URL); err != nil {
		t.Errorf("Measure with LPM_CA_FILE = %v, want the certificate trusted", err)
	}
}

func TestNewFetcherRejectsAnUnusableCertificateAuthority(t *testing.T) {
	t.Setenv("LPM_CA_FILE", filepath.Join(t.TempDir(), "absent.pem"))
	if _, err := NewFetcher(); err == nil {
		t.Error("NewFetcher accepted a LPM_CA_FILE that does not exist")
	}

	path := filepath.Join(t.TempDir(), "not-a-cert.pem")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LPM_CA_FILE", path)
	if _, err := NewFetcher(); err == nil || !strings.Contains(err.Error(), "no certificate") {
		t.Errorf("NewFetcher = %v, want it to report a file holding no certificate", err)
	}
}

func encodePEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestUnpackWritesThePackageRoot(t *testing.T) {
	for name, archive := range map[string][]byte{
		"tar.gz": tarGz(t,
			entry{name: "libshape_tools.so", body: "code", mode: 0o755},
			entry{name: "docs", dir: true},
			entry{name: "docs/README.md", body: "read me"},
		),
		"zip": zipArchive(t,
			entry{name: "libshape_tools.so", body: "code", mode: 0o755},
			entry{name: "docs", dir: true},
			entry{name: "docs/README.md", body: "read me"},
		),
	} {
		dest := t.TempDir()
		if err := Unpack(archive, name, dest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		body, err := os.ReadFile(filepath.Join(dest, "libshape_tools.so"))
		if err != nil || string(body) != "code" {
			t.Errorf("%s: library = %q, %v", name, body, err)
		}
		if body, err := os.ReadFile(filepath.Join(dest, "docs", "README.md")); err != nil ||
			string(body) != "read me" {
			t.Errorf("%s: nested file = %q, %v", name, body, err)
		}

		// Windows has no executable bit to carry through.
		if runtime.GOOS == "windows" {
			continue
		}
		info, err := os.Stat(filepath.Join(dest, "libshape_tools.so"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s: the library lost its executable bit", name)
		}
	}
}

// `tar czf pkg.tar.gz pkg/` wraps everything in one directory, and that
// directory is not part of the package.
func TestUnpackStripsOneWrappingDirectory(t *testing.T) {
	for name, archive := range map[string][]byte{
		"tar.gz": tarGz(t,
			entry{name: "shape-tools-1.2.3", dir: true},
			entry{name: "shape-tools-1.2.3/libshape_tools.so", body: "code"},
			entry{name: "shape-tools-1.2.3/docs/README.md", body: "read me"},
		),
		"zip": zipArchive(t,
			entry{name: "shape-tools-1.2.3", dir: true},
			entry{name: "shape-tools-1.2.3/libshape_tools.so", body: "code"},
			entry{name: "shape-tools-1.2.3/docs/README.md", body: "read me"},
		),
	} {
		dest := t.TempDir()
		if err := Unpack(archive, name, dest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dest, "libshape_tools.so")); err != nil {
			t.Errorf("%s: the wrapping directory was kept: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dest, "shape-tools-1.2.3")); err == nil {
			t.Errorf("%s: the wrapping directory is still there", name)
		}
	}
}

// Two directories at the top are two directories the package wanted.
func TestUnpackKeepsTwoTopLevelDirectories(t *testing.T) {
	archive := tarGz(t,
		entry{name: "lib/libshape_tools.so", body: "code"},
		entry{name: "share/shapes.cdl", body: "fn main() {}"},
	)

	dest := t.TempDir()
	if err := Unpack(archive, "tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"lib/libshape_tools.so", "share/shapes.cdl"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(path))); err != nil {
			t.Errorf("%s is missing: %v", path, err)
		}
	}
}

func TestUnpackRefusesToWriteOutsideTheDestination(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")

	for name, archive := range map[string][]byte{
		"tar traversal": tarGz(t,
			entry{name: "lib.so", body: "code"},
			entry{name: "../escaped", body: "no"},
		),
		"tar symlink": tarGz(t,
			entry{name: "lib.so", body: "code"},
			entry{name: "link", link: outside},
		),
		"zip traversal": zipArchive(t,
			entry{name: "lib.so", body: "code"},
			entry{name: "../escaped", body: "no"},
		),
		"zip backslash": zipArchive(t,
			entry{name: "lib.so", body: "code"},
			entry{name: `..\escaped`, body: "no"},
		),
	} {
		dest := t.TempDir()
		err := Unpack(archive, name, dest)
		if err == nil {
			t.Errorf("%s: Unpack accepted an entry that writes outside the package", name)
		}
		if _, statErr := os.Stat(outside); statErr == nil {
			t.Fatalf("%s: something was written outside the destination", name)
		}
	}
}

// An entry naming an absolute path is not a traversal: it unpacks relative to
// the package directory like every other entry, and nothing reaches the root.
func TestUnpackKeepsAnAbsoluteEntryInsideThePackage(t *testing.T) {
	dest := t.TempDir()
	archive := tarGz(t,
		entry{name: "lib.so", body: "code"},
		entry{name: "/etc/passwd", body: "no"},
	)

	if err := Unpack(archive, "tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc", "passwd")); err != nil {
		t.Errorf("the absolute entry did not land under the package: %v", err)
	}
}

func TestUnpackRejectsUnknownArchives(t *testing.T) {
	dest := t.TempDir()
	for name, body := range map[string][]byte{
		"plain text": []byte("this is not an archive"),
		"empty":      {},
	} {
		if err := Unpack(body, name, dest); err == nil ||
			!strings.Contains(err.Error(), "neither a .tar.gz nor a .zip") {
			t.Errorf("%s: Unpack = %v, want a format refusal", name, err)
		}
	}

	// A digest check already rejects a damaged download; a body that stops
	// mid-stream is reported rather than half-unpacked.
	whole := tarGz(t, entry{name: "lib.so", body: strings.Repeat("code", 2000)})
	if err := Unpack(whole[:len(whole)/2], "truncated", dest); err == nil {
		t.Error("Unpack accepted an archive that stops mid-stream")
	}
}

func TestStripFindsTheWrappingDirectory(t *testing.T) {
	for _, c := range []struct {
		paths []string
		want  string
	}{
		{[]string{"pkg/a", "pkg/b/c"}, "pkg"},
		{[]string{"pkg/", "pkg/a"}, "pkg"},
		{[]string{"pkg", "pkg/a"}, ""}, // a file named pkg, not a directory
		{[]string{"a/x", "b/y"}, ""},
		{[]string{"lib.so"}, ""},
		{nil, ""},
	} {
		if got := strip(c.paths); got != c.want {
			t.Errorf("strip(%v) = %q, want %q", c.paths, got, c.want)
		}
	}
}

func TestEntryPathReportsAMemberOutsideTheWrapper(t *testing.T) {
	_, _, err := entryPath(t.TempDir(), "elsewhere/file", "pkg")
	if err == nil || !strings.Contains(err.Error(), "sits outside") {
		t.Errorf("entryPath = %v, want a report that the member escapes the wrapper", err)
	}

	// The wrapper's own directory entry contributes nothing to unpack.
	if _, ok, err := entryPath(t.TempDir(), "pkg", "pkg"); err != nil || ok {
		t.Errorf("entryPath(pkg, pkg) = %v, %v, want it skipped", ok, err)
	}
}

func TestUnpackReportsAnUnwritableDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not take a write bit off a directory on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root writes into a directory with no write bit")
	}

	dest := t.TempDir()
	if err := os.Chmod(dest, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dest, 0o700) })

	archive := tarGz(t, entry{name: "lib.so", body: "code"})
	if err := Unpack(archive, "tar.gz", dest); err == nil {
		t.Error("Unpack succeeded into a directory it cannot write to")
	}
}

func TestUnpackRejectsAZipHoldingSomethingOtherThanAFile(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	writer, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(writer, "/etc/passwd")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Unpack(buf.Bytes(), "zip", t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "regular files and directories only") {
		t.Errorf("Unpack = %v, want a refusal to unpack a symlink", err)
	}
}
