package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// markerName is written into a package directory once its archive has been
// verified and unpacked. A directory without it is a directory an interrupted
// run left behind, so lpm treats it as absent and fetches again.
const markerName = ".lpm"

// Cache is the unpacked copy of every artifact this machine has verified.
type Cache struct{ Root string }

// OpenCache finds the cache root. LPM_CACHE_DIR overrides the platform
// default, which is where the operating system already keeps discardable
// per-user data.
func OpenCache() (*Cache, error) {
	root := os.Getenv("LPM_CACHE_DIR")
	if root == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("find cache dir: %w", err)
		}
		root = filepath.Join(dir, "lpm")
	}

	// The --json report gives absolute paths, and a host CLI resolves them
	// from its own working directory, not from lpm's.
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve cache dir: %w", err)
	}
	return &Cache{Root: root}, nil
}

// Dir is where one artifact unpacks to. The target is part of the path, so a
// machine that installs for two targets keeps both.
func (c *Cache) Dir(name, version, target string) string {
	return filepath.Join(c.Root, "pkgs", name, version, target)
}

// Has reports whether dir holds a verified unpack of the artifact with this
// digest. A marker naming another digest means the artifact was republished,
// so the answer is no and the caller fetches again.
func (c *Cache) Has(dir, sha256 string) bool {
	got, err := os.ReadFile(filepath.Join(dir, markerName))
	return err == nil && strings.TrimSpace(string(got)) == sha256
}

// Store replaces dir with the unpacked archive and marks it verified. It
// unpacks beside the destination and renames, so a failure partway through
// leaves no directory a later run would trust.
func (c *Cache) Store(dir, sha256 string, archive []byte, source string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}

	staging, err := os.MkdirTemp(parent, ".lpm-unpack-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	defer os.RemoveAll(staging)

	if err := Unpack(archive, source, staging); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, markerName), []byte(sha256+"\n"), 0o644); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	// Rename onto an existing directory fails, and a leftover from an
	// interrupted run is exactly what we are replacing.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("replace %s: %w", dir, err)
	}
	if err := os.Rename(staging, dir); err != nil {
		return fmt.Errorf("replace %s: %w", dir, err)
	}
	return nil
}

// Files lists the top-level entries of an unpacked package, sorted, without
// the marker lpm wrote itself.
func (c *Cache) Files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name() != markerName {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}
