package src

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
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
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9_-]*[a-zA-Z0-9])?$`)

// Allows '.' and stays URL-safe.
var packageNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// Wider than semver. Build metadata and leading 'v'.
var versionPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9._+-]*[a-zA-Z0-9])?$`)

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
func checkReleaseURL(fe FieldErrors, raw string) {
	if raw == "" {
		fe.add("url", "is required")
		return
	}
	if len(raw) > releaseURLMaxLen {
		fe.add("url", fmt.Sprintf("must be at most %d bytes", releaseURLMaxLen))
		return
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		fe.add("url", "must be a valid URL")
		return
	}

	switch {
	case parsed.Scheme != "https":
		fe.add("url", "must use https")
	case parsed.Host == "":
		fe.add("url", "must include a host")
	case parsed.User != nil:
		fe.add("url", "must not embed credentials")
	case parsed.Fragment != "":
		fe.add("url", "must not include a fragment")
	}
}

func (n *NewRelease) Validate() FieldErrors {
	n.URL = strings.TrimSpace(n.URL)
	n.Version = strings.TrimSpace(n.Version)
	n.Description = strings.TrimSpace(n.Description)

	fe := FieldErrors{}

	checkReleaseURL(fe, n.URL)

	switch {
	case n.Version == "":
		fe.add("version", "is required")
	case utf8.RuneCountInString(n.Version) > versionMaxLen:
		fe.add("version", fmt.Sprintf("must be at most %d characters", versionMaxLen))
	case !versionPattern.MatchString(n.Version):
		fe.add("version", "may contain only letters, digits, '.', '+', '-' and '_', and must start and end with a letter or digit")
	}

	if utf8.RuneCountInString(n.Description) > descriptionMaxLen {
		fe.add("description", fmt.Sprintf("must be at most %d characters", descriptionMaxLen))
	}

	return fe
}
