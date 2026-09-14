package internal

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ReportSchema is the version of the --json document. A host CLI reads it
// before anything else and refuses a schema it does not know.
const ReportSchema = 1

// InstalledPackage is one package as it now sits on disk.
type InstalledPackage struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Target   string `json:"target"`
	Dir      string `json:"dir"`
	// Top-level entries of Dir, so a host CLI can find the library file
	// without knowing the packaging convention.
	Files        []string          `json:"files"`
	Dependencies map[string]string `json:"dependencies"`
}

// Report is what --json prints.
type Report struct {
	Schema   int                `json:"schema"`
	Lock     string             `json:"lock"`
	Packages []InstalledPackage `json:"packages"`
}

// Install puts every chosen artifact in the cache and reports where it
// landed. A cached copy whose marker matches is reused; anything else is
// downloaded, verified, and unpacked. Offline never reaches the network, so a
// cache miss is the end of the run.
func Install(cache *Cache, fetcher *Fetcher, offline bool, choices []Choice) ([]InstalledPackage, error) {
	installed := make([]InstalledPackage, 0, len(choices))

	for _, choice := range choices {
		dir := cache.Dir(choice.Name, choice.Version, choice.Artifact.Target)

		if !cache.Has(dir, choice.Artifact.SHA256) {
			if offline {
				return nil, Fail(ExitOffline,
					"%s %s (%s) is not in the cache, and --offline downloads nothing",
					choice.Name, choice.Version, choice.Artifact.Target)
			}
			archive, err := fetcher.Download(choice.Artifact)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", choice.Name, choice.Version, err)
			}
			if err := cache.Store(dir, choice.Artifact.SHA256, archive, choice.Artifact.URL); err != nil {
				return nil, fmt.Errorf("%s %s: %w", choice.Name, choice.Version, err)
			}
		}

		files, err := cache.Files(dir)
		if err != nil {
			return nil, err
		}

		installed = append(installed, InstalledPackage{
			Name:         choice.Name,
			Version:      choice.Version,
			Platform:     choice.Platform,
			Target:       choice.Artifact.Target,
			Dir:          dir,
			Files:        files,
			Dependencies: choice.Dependencies,
		})
	}

	return installed, nil
}

// LockFor builds the lock a resolution implies. Artifacts recorded for other
// targets survive as long as the version does, so a machine that installs for
// one target does not drop what another machine verified.
func LockFor(previous *Lock, registry string, choices []Choice) *Lock {
	lock := &Lock{Version: LockVersion, Packages: make([]LockedPackage, 0, len(choices))}

	for _, choice := range choices {
		artifacts := map[string]string{}
		if pin, ok := previous.Find(choice.Name); ok && pin.Version == choice.Version {
			maps.Copy(artifacts, pin.Artifacts)
		}
		artifacts[choice.Artifact.Target] = "sha256:" + choice.Artifact.SHA256

		dependencies := make([]string, 0, len(choice.Dependencies))
		for _, name := range slices.Sorted(maps.Keys(choice.Dependencies)) {
			dependencies = append(dependencies, name+" "+choice.Dependencies[name])
		}

		lock.Packages = append(lock.Packages, LockedPackage{
			Name:         choice.Name,
			Version:      choice.Version,
			Platform:     choice.Platform,
			Registry:     registry,
			Dependencies: dependencies,
			Artifacts:    artifacts,
		})
	}

	slices.SortFunc(lock.Packages, func(a, b LockedPackage) int {
		return strings.Compare(a.Name, b.Name)
	})
	return lock
}
