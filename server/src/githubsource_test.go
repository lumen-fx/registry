package src

import (
	"testing"
	"time"
)

func TestParseReleaseURL(t *testing.T) {
	cases := []struct {
		name  string
		url   string
		ref   releaseRef
		asset string
		ok    bool
	}{
		{
			name:  "release asset",
			url:   "https://github.com/lumen-fx/strutil/releases/download/v1.2.3/strutil-linux-x86_64.tar.gz",
			ref:   releaseRef{Owner: "lumen-fx", Repo: "strutil", Tag: "v1.2.3"},
			asset: "strutil-linux-x86_64.tar.gz",
			ok:    true,
		},
		{
			// Tags carry slashes often enough that dropping them would lose
			// whole repositories.
			name:  "tag with slashes",
			url:   "https://github.com/o/r/releases/download/release/2026-09/pkg.tgz",
			ref:   releaseRef{Owner: "o", Repo: "r", Tag: "release/2026-09"},
			asset: "pkg.tgz",
			ok:    true,
		},
		{
			name:  "escaped tag",
			url:   "https://github.com/o/r/releases/download/v1.0%2Bbuild.2/pkg.tgz",
			ref:   releaseRef{Owner: "o", Repo: "r", Tag: "v1.0+build.2"},
			asset: "pkg.tgz",
			ok:    true,
		},
		{name: "another host", url: "https://example.test/o/r/releases/download/v1/pkg.tgz"},
		{name: "a lookalike host", url: "https://github.com.example.test/o/r/releases/download/v1/pkg.tgz"},
		{name: "not a release", url: "https://github.com/o/r/archive/refs/tags/v1.tar.gz"},
		{name: "no asset", url: "https://github.com/o/r/releases/download/v1"},
		{name: "not https", url: "http://github.com/o/r/releases/download/v1/pkg.tgz"},
		{name: "not a url", url: "://"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref, asset, ok := parseReleaseURL(c.url)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !c.ok {
				return
			}
			if ref != c.ref {
				t.Errorf("ref = %+v, want %+v", ref, c.ref)
			}
			if asset != c.asset {
				t.Errorf("asset = %q, want %q", asset, c.asset)
			}
		})
	}
}

// A release may host some archives on GitHub and others elsewhere. The
// repository is whichever artifact names one, taken in the order the store
// returns them so the answer does not move between requests.
func TestReleaseRefForSkipsArtifactsHostedElsewhere(t *testing.T) {
	release := Release{Artifacts: []Artifact{
		{Target: "any", URL: "https://mirror.test/pkg.tgz"},
		{Target: "linux-x86_64", URL: "https://github.com/o/r/releases/download/v2/pkg.tgz"},
	}}

	ref, ok := releaseRefFor(release)
	if !ok {
		t.Fatal("releaseRefFor found no repository")
	}
	if want := (releaseRef{Owner: "o", Repo: "r", Tag: "v2"}); ref != want {
		t.Errorf("ref = %+v, want %+v", ref, want)
	}

	if _, ok := releaseRefFor(Release{Artifacts: []Artifact{{URL: "https://mirror.test/pkg.tgz"}}}); ok {
		t.Error("a release hosted entirely elsewhere resolved to a repository")
	}
}

// Freshness is measured from the last attempt, and how long an answer lasts
// depends on what the answer was.
func TestReadmeExpiry(t *testing.T) {
	now := time.Now()

	cases := []struct {
		status string
		age    time.Duration
		want   bool
	}{
		{statusOK, time.Hour, false},
		{statusOK, 25 * time.Hour, true},
		{statusMissing, time.Hour, false},
		{statusMissing, 7 * time.Hour, true},
		{statusUnavailable, time.Minute, false},
		{statusUnavailable, 20 * time.Minute, true},
	}

	for _, c := range cases {
		entry := readmeCache{Status: c.status, CheckedAt: now.Add(-c.age)}
		if got := readmeExpired(entry, now); got != c.want {
			t.Errorf("%s after %s: expired = %v, want %v", c.status, c.age, got, c.want)
		}
	}
}
