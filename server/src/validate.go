package src

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lumen-fx/registry/cli/req"
)

const (
	usernameMaxLen = 39 // GitHub's own login limit

	tokenNameMaxLen = 100

	filterValueMaxLen = 200

	platformMaxLen    = 32
	packageNameMaxLen = 128
	descriptionMaxLen = 2000
	versionMaxLen     = 64
	releaseURLMaxLen  = 2048

	requirementMaxLen = 128
	requirementsMax   = 100
	artifactsMax      = 32
)

// The platforms a package may target. A package belongs to one product, and a
// client asks the registry for packages of the product it installs for.
var platforms = []string{"lumen", "candela"}

// The targets an artifact may build for. `any` runs everywhere, and a
// target-specific artifact wins over it.
var targets = []string{
	"linux-x86_64",
	"linux-aarch64",
	"macos-x86_64",
	"macos-aarch64",
	"windows-x86_64",
	"windows-aarch64",
	"any",
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9_-]*[a-zA-Z0-9])?$`)

// Allows '.' and stays URL-safe.
var packageNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// Semver 2.0.0, the published pattern: MAJOR.MINOR.PATCH with an optional
// pre-release and optional build metadata. A client compares versions to
// resolve a requirement, so a version it cannot order is a version it cannot
// install.
var versionPattern = regexp.MustCompile(`^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)` +
	`(?:-(?:(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+(?:[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type FieldErrors map[string]string

func (fe FieldErrors) add(field, msg string) {
	if _, dup := fe[field]; !dup {
		fe[field] = msg
	}
}

func (fe FieldErrors) ok() bool { return len(fe) == 0 }

// Token names label a credential in a list, nothing more.
func (t *NewToken) Validate() FieldErrors {
	t.Name = strings.TrimSpace(t.Name)

	fe := FieldErrors{}
	switch n := utf8.RuneCountInString(t.Name); {
	case t.Name == "":
		fe.add("name", "is required")
	case n > tokenNameMaxLen:
		fe.add("name", fmt.Sprintf("must be at most %d characters", tokenNameMaxLen))
	}
	return fe
}

// Validate uses query parameter names, not struct field names.
func (f *PackageFilter) Validate() FieldErrors {
	f.Platform = strings.TrimSpace(f.Platform)
	f.Name = strings.TrimSpace(f.Name)
	f.Search = strings.TrimSpace(f.Search)
	f.Username = strings.TrimSpace(f.Username)
	f.Version = strings.TrimSpace(f.Version)

	fe := FieldErrors{}
	for _, term := range []struct {
		field string
		value string
	}{
		{"platform", f.Platform},
		{"name", f.Name},
		{"q", f.Search},
		{"username", f.Username},
		{"version", f.Version},
	} {
		if utf8.RuneCountInString(term.value) > filterValueMaxLen {
			fe.add(term.field, fmt.Sprintf("must be at most %d characters", filterValueMaxLen))
		}
	}
	return fe
}

func (n *NewPackage) Validate() FieldErrors {
	n.Platform = strings.TrimSpace(n.Platform)
	n.Name = strings.TrimSpace(n.Name)
	n.Description = strings.TrimSpace(n.Description)

	fe := FieldErrors{}

	switch {
	case n.Platform == "":
		fe.add("platform", "is required")
	case utf8.RuneCountInString(n.Platform) > platformMaxLen:
		fe.add("platform", fmt.Sprintf("must be at most %d characters", platformMaxLen))
	case !slices.Contains(platforms, n.Platform):
		fe.add("platform", "must be one of "+strings.Join(platforms, ", "))
	}

	switch {
	case n.Name == "":
		fe.add("name", "is required")
	case utf8.RuneCountInString(n.Name) > packageNameMaxLen:
		fe.add("name", fmt.Sprintf("must be at most %d characters", packageNameMaxLen))
	case !packageNamePattern.MatchString(n.Name):
		fe.add("name", "may contain only letters, digits, '.', '-' and '_', and must start and end with a letter or digit")
	}

	if utf8.RuneCountInString(n.Description) > descriptionMaxLen {
		fe.add("description", fmt.Sprintf("must be at most %d characters", descriptionMaxLen))
	}

	return fe
}

// https only. Clients fetch this URL to install code.
func checkArtifactURL(fe FieldErrors, field, raw string) {
	if raw == "" {
		fe.add(field, "is required")
		return
	}
	if len(raw) > releaseURLMaxLen {
		fe.add(field, fmt.Sprintf("must be at most %d bytes", releaseURLMaxLen))
		return
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		fe.add(field, "must be a valid URL")
		return
	}

	switch {
	case parsed.Scheme != "https":
		fe.add(field, "must use https")
	case parsed.Host == "":
		fe.add(field, "must include a host")
	case parsed.User != nil:
		fe.add(field, "must not embed credentials")
	case parsed.Fragment != "":
		fe.add(field, "must not include a fragment")
	}
}

// checkRequirements validates a name-to-requirement map: the names a client
// looks the entry up by, and the requirement strings it resolves. Both sides
// read a requirement through the same package, so a release the registry
// accepts is one a client can resolve.
func checkRequirements(fe FieldErrors, field string, reqs Requirements) {
	if len(reqs) > requirementsMax {
		fe.add(field, fmt.Sprintf("must hold at most %d entries", requirementsMax))
		return
	}

	// Sorted, so the reported field is the same on every run.
	for _, name := range slices.Sorted(maps.Keys(reqs)) {
		key := field + "." + name
		switch {
		case utf8.RuneCountInString(name) > packageNameMaxLen:
			fe.add(key, fmt.Sprintf("name must be at most %d characters", packageNameMaxLen))
		case !packageNamePattern.MatchString(name):
			fe.add(key, "name may contain only letters, digits, '.', '-' and '_', and must start and end with a letter or digit")
		}

		requirement := strings.TrimSpace(reqs[name])
		switch {
		case requirement == "":
			fe.add(key, "version requirement is required")
		case utf8.RuneCountInString(requirement) > requirementMaxLen:
			fe.add(key, fmt.Sprintf("version requirement must be at most %d characters", requirementMaxLen))
		default:
			if _, err := req.Parse(requirement); err != nil {
				fe.add(key, fmt.Sprintf("%q is not a version requirement, e.g. ^1.2, ~1.2, =1.2.3, >=1, <2", requirement))
				continue
			}
			reqs[name] = requirement
		}
	}
}

func checkArtifacts(fe FieldErrors, artifacts []NewArtifact) {
	switch {
	case len(artifacts) == 0:
		fe.add("artifacts", "must hold at least one artifact")
		return
	case len(artifacts) > artifactsMax:
		fe.add("artifacts", fmt.Sprintf("must hold at most %d artifacts", artifactsMax))
		return
	}

	seen := map[string]bool{}
	for i := range artifacts {
		a := &artifacts[i]
		a.Target = strings.TrimSpace(a.Target)
		a.URL = strings.TrimSpace(a.URL)
		a.SHA256 = strings.TrimSpace(a.SHA256)

		field := fmt.Sprintf("artifacts[%d]", i)

		switch {
		case a.Target == "":
			fe.add(field+".target", "is required")
		case !slices.Contains(targets, a.Target):
			fe.add(field+".target", "must be one of "+strings.Join(targets, ", "))
		case seen[a.Target]:
			fe.add(field+".target", "is already used by an earlier artifact")
		default:
			seen[a.Target] = true
		}

		checkArtifactURL(fe, field+".url", a.URL)

		switch {
		case a.SHA256 == "":
			fe.add(field+".sha256", "is required")
		case !sha256Pattern.MatchString(a.SHA256):
			fe.add(field+".sha256", "must be 64 lowercase hex characters")
		}

		if a.Size <= 0 {
			fe.add(field+".size", "must be the artifact's size in bytes")
		}
	}
}

func (n *NewRelease) Validate() FieldErrors {
	n.Version = strings.TrimSpace(n.Version)
	n.Description = strings.TrimSpace(n.Description)
	if n.Dependencies == nil {
		n.Dependencies = Requirements{}
	}
	if n.Requires == nil {
		n.Requires = Requirements{}
	}

	fe := FieldErrors{}

	switch {
	case n.Version == "":
		fe.add("version", "is required")
	case utf8.RuneCountInString(n.Version) > versionMaxLen:
		fe.add("version", fmt.Sprintf("must be at most %d characters", versionMaxLen))
	case !versionPattern.MatchString(n.Version):
		fe.add("version", "must be semver: MAJOR.MINOR.PATCH, with an optional pre-release and build metadata")
	}

	if utf8.RuneCountInString(n.Description) > descriptionMaxLen {
		fe.add("description", fmt.Sprintf("must be at most %d characters", descriptionMaxLen))
	}

	checkRequirements(fe, "dependencies", n.Dependencies)
	checkRequirements(fe, "requires", n.Requires)
	checkArtifacts(fe, n.Artifacts)

	return fe
}
