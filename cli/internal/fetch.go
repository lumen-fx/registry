package internal

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// An artifact is a package, not a disk image. The cap stops a wrong URL from
// filling the cache before the size check can reject it.
const maxArtifactBytes = 1 << 30 // 1 GiB

// Fetcher downloads artifacts and checks them against what the registry
// published.
type Fetcher struct{ HTTP *http.Client }

// NewFetcher builds the client artifact downloads use. LPM_CA_FILE adds a
// certificate authority to the system set, for a registry or an artifact host
// behind a private CA. Nothing is trusted by default that was not already.
func NewFetcher() (*Fetcher, error) {
	client := &http.Client{Timeout: 5 * time.Minute}

	caFile := os.Getenv("LPM_CA_FILE")
	if caFile == "" {
		return &Fetcher{HTTP: client}, nil
	}

	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read LPM_CA_FILE: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("LPM_CA_FILE %s holds no certificate", caFile)
	}

	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	return &Fetcher{HTTP: client}, nil
}

// Download fetches one artifact and returns its bytes only when they are the
// size and the digest the registry published. A mismatch returns an error and
// nothing else, so an artifact that fails here never reaches the cache.
func (f *Fetcher) Download(artifact Artifact) ([]byte, error) {
	if artifact.Size <= 0 || artifact.Size > maxArtifactBytes {
		return nil, fmt.Errorf("%s: the registry lists an artifact size of %d bytes",
			artifact.URL, artifact.Size)
	}

	res, err := f.HTTP.Get(artifact.URL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", artifact.URL, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: the host answered %d", artifact.URL, res.StatusCode)
	}

	// One byte over the declared size is enough to tell too-big from exact.
	body, err := io.ReadAll(io.LimitReader(res.Body, artifact.Size+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", artifact.URL, err)
	}
	if int64(len(body)) != artifact.Size {
		return nil, fmt.Errorf("%s is %d bytes, and the registry lists %d",
			artifact.URL, len(body), artifact.Size)
	}

	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != artifact.SHA256 {
		return nil, fmt.Errorf("%s hashes to %s, and the registry lists %s",
			artifact.URL, got, artifact.SHA256)
	}

	return body, nil
}

// Measure downloads an artifact to work out what the registry should record
// for it. Publishing computes the digest from the bytes a client will fetch,
// from the URL a client will fetch them from, so the registry never records a
// digest of something else.
func (f *Fetcher) Measure(url string) (string, int64, error) {
	res, err := f.HTTP.Get(url)
	if err != nil {
		return "", 0, fmt.Errorf("download %s: %w", url, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("download %s: the host answered %d", url, res.StatusCode)
	}

	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(res.Body, maxArtifactBytes+1))
	if err != nil {
		return "", 0, fmt.Errorf("download %s: %w", url, err)
	}
	switch {
	case size == 0:
		return "", 0, fmt.Errorf("%s is empty", url)
	case size > maxArtifactBytes:
		return "", 0, fmt.Errorf("%s is larger than lpm installs", url)
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// Unpack writes an archive into dest. The archive's root is the package root;
// an archive whose entries all sit under one directory has that directory
// stripped, because that is what `tar czf pkg.tar.gz pkg/` produces.
//
// source names the archive in errors. Its suffix picks the format only when
// the bytes do not say.
func Unpack(archive []byte, source, dest string) error {
	switch {
	case bytes.HasPrefix(archive, []byte{0x1f, 0x8b}):
		return unpackTarGz(archive, source, dest)
	case bytes.HasPrefix(archive, []byte("PK\x03\x04")):
		return unpackZip(archive, source, dest)
	default:
		return fmt.Errorf("%s is neither a .tar.gz nor a .zip", source)
	}
}

// strip finds the single directory every entry sits under, or "" when the
// archive already has the package at its root. Directory names arrive with a
// trailing slash, so the wrapper's own entry does not read as a file sitting
// at the top.
func strip(paths []string) string {
	var root string
	for _, p := range paths {
		slash := strings.Index(p, "/")
		if slash < 0 {
			return "" // a file is already at the top
		}
		switch segment := p[:slash]; {
		case root == "":
			root = segment
		case root != segment:
			return "" // more than one directory at the top
		}
	}
	return root
}

// archiveName normalises a member name: one separator, and a trailing slash
// on a directory so strip can tell it from a file.
func archiveName(name string, isDir bool) string {
	clean := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	if isDir && !strings.HasSuffix(clean, "/") {
		clean += "/"
	}
	return clean
}

// entryPath turns an archive member name into the file to write, and refuses
// anything that would write outside dest. A ".." anywhere in the name is
// rejected rather than resolved away: an archive has no business naming a
// parent, and silently rewriting the name hides that it tried.
//
// A leading separator is not a traversal. It unpacks relative to the package
// directory like every other name.
func entryPath(dest, name, wrapper string) (string, bool, error) {
	slashed := strings.ReplaceAll(name, `\`, "/")
	for _, segment := range strings.Split(slashed, "/") {
		if segment == ".." {
			return "", false, fmt.Errorf("archive entry %q would write outside the package directory", name)
		}
	}

	clean := strings.TrimPrefix(path.Clean("/"+slashed), "/")

	if wrapper != "" {
		if clean == wrapper {
			return "", false, nil
		}
		trimmed, ok := strings.CutPrefix(clean, wrapper+"/")
		if !ok {
			return "", false, fmt.Errorf("archive entry %q sits outside %q", name, wrapper)
		}
		clean = trimmed
	}

	if clean == "" || clean == "." {
		return "", false, nil
	}

	target := filepath.Join(dest, filepath.FromSlash(clean))
	// Everything above should have made this impossible; a package that
	// escapes the cache is worth one more comparison.
	if !strings.HasPrefix(target, dest+string(filepath.Separator)) {
		return "", false, fmt.Errorf("archive entry %q would write outside the package directory", name)
	}
	return target, true, nil
}

func writeFile(target string, mode os.FileMode, r io.Reader, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}

	// Executables matter: a module's library file has to stay runnable.
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, io.LimitReader(r, limit)); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return file.Close()
}

func unpackTarGz(archive []byte, source, dest string) error {
	names, err := tarNames(archive, source)
	if err != nil {
		return err
	}
	wrapper := strip(names)

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", source, err)
		}

		// A symlink can point anywhere, including outside the cache, and no
		// package needs one.
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeDir:
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		default:
			return fmt.Errorf("%s holds %q, and lpm unpacks regular files and directories only",
				source, header.Name)
		}

		target, ok, err := entryPath(dest, header.Name, wrapper)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			continue
		}
		if err := writeFile(target, header.FileInfo().Mode(), reader, maxArtifactBytes); err != nil {
			return err
		}
	}
}

// tarNames reads the member names, so the wrapping directory is known before
// anything is written.
func tarNames(archive []byte, source string) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	defer gz.Close()

	var names []string
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", source, err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeXHeader {
			continue
		}
		names = append(names, archiveName(header.Name, header.Typeflag == tar.TypeDir))
	}
}

func unpackZip(archive []byte, source, dest string) error {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}

	names := make([]string, 0, len(reader.File))
	for _, f := range reader.File {
		names = append(names, archiveName(f.Name, f.FileInfo().IsDir()))
	}
	wrapper := strip(names)

	for _, f := range reader.File {
		mode := f.Mode()
		if mode&os.ModeType != 0 && !mode.IsDir() {
			return fmt.Errorf("%s holds %q, and lpm unpacks regular files and directories only",
				source, f.Name)
		}

		target, ok, err := entryPath(dest, f.Name, wrapper)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		if mode.IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			continue
		}

		entry, err := f.Open()
		if err != nil {
			return fmt.Errorf("read %s: %w", source, err)
		}
		err = writeFile(target, mode, entry, maxArtifactBytes)
		entry.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
