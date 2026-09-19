package src

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// stubSource wires a githubSource at a test server, the way production wires
// it at api.github.com.
func stubSource(t *testing.T, handler http.HandlerFunc) githubSource {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return githubSource{apiURL: server.URL, token: "test-token", client: server.Client()}
}

func readmePayload(markdown string) string {
	return fmt.Sprintf(`{"content":%q,"encoding":"base64","html_url":"https://github.com/o/r/blob/v1/README.md"}`,
		base64.StdEncoding.EncodeToString([]byte(markdown)))
}

var testRef = releaseRef{Owner: "o", Repo: "r", Tag: "v1"}

func TestReadmeIsReadAtTheTag(t *testing.T) {
	var asked *http.Request
	source := stubSource(t, func(w http.ResponseWriter, r *http.Request) {
		asked = r
		fmt.Fprint(w, readmePayload("# hello"))
	})

	markdown, url, err := source.readme(context.Background(), testRef)
	if err != nil {
		t.Fatalf("readme: %v", err)
	}
	if markdown != "# hello" {
		t.Errorf("markdown = %q", markdown)
	}
	if url != "https://github.com/o/r/blob/v1/README.md" {
		t.Errorf("source = %q", url)
	}
	if got := asked.URL.Query().Get("ref"); got != "v1" {
		t.Errorf("ref = %q, want the release tag", got)
	}
	// A token lifts the anonymous rate limit, and only a sent one does.
	if got := asked.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
}

// Everything GitHub can answer that is not a README, and what each one means
// to a reader: nothing to show, or something worth retrying.
func TestReadmeFailures(t *testing.T) {
	oversized := strings.Repeat("a", readmeMaxBytes+1)

	cases := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		want    error
	}{
		{name: "no readme", status: http.StatusNotFound, want: ErrNoReadme},
		{name: "too large for the api", status: http.StatusOK, body: `{"content":"","encoding":"none"}`, want: ErrNoReadme},
		{name: "too large to render", status: http.StatusOK, body: readmePayload(oversized), want: ErrNoReadme},
		{name: "not text", status: http.StatusOK, body: `{"content":"//4=","encoding":"base64"}`, want: ErrNoReadme},
		{
			name:    "rate limited",
			status:  http.StatusForbidden,
			headers: map[string]string{"X-RateLimit-Remaining": "0"},
			want:    ErrGitHubLimited,
		},
		{name: "server error", status: http.StatusBadGateway},
		{name: "not json", status: http.StatusOK, body: `{`},
		{name: "not base64", status: http.StatusOK, body: `{"content":"!!!","encoding":"base64"}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := stubSource(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			})

			_, _, err := source.readme(context.Background(), testRef)
			if err == nil {
				t.Fatal("readme returned no error")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("error = %v, want %v", err, c.want)
			}
		})
	}
}

func TestAssetDownloads(t *testing.T) {
	source := stubSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases/tags/v1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"assets":[{"name":"pkg.tgz","download_count":7},{"name":"other.tgz","download_count":0}]}`)
	})

	counts, err := source.assetDownloads(context.Background(), testRef)
	if err != nil {
		t.Fatalf("assetDownloads: %v", err)
	}
	if counts["pkg.tgz"] != 7 || counts["other.tgz"] != 0 || len(counts) != 2 {
		t.Errorf("counts = %v", counts)
	}
}

func TestAssetDownloadsFailures(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		want    error
	}{
		{name: "no such release", status: http.StatusNotFound, want: ErrNoGitHubRef},
		{
			name:    "rate limited",
			status:  http.StatusTooManyRequests,
			headers: map[string]string{"X-RateLimit-Remaining": "0"},
			want:    ErrGitHubLimited,
		},
		{name: "server error", status: http.StatusBadGateway},
		{name: "not json", status: http.StatusOK, body: `{"assets":`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := stubSource(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			})

			_, err := source.assetDownloads(context.Background(), testRef)
			if err == nil {
				t.Fatal("assetDownloads returned no error")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("error = %v, want %v", err, c.want)
			}
		})
	}
}

// An unreachable GitHub is an error, not an empty answer.
func TestGitHubUnreachable(t *testing.T) {
	source := githubSource{apiURL: "http://127.0.0.1:1", client: &http.Client{Timeout: time.Second}}

	if _, _, err := source.readme(context.Background(), testRef); err == nil {
		t.Error("readme returned nil against an unreachable host")
	}
	if _, err := source.assetDownloads(context.Background(), testRef); err == nil {
		t.Error("assetDownloads returned nil against an unreachable host")
	}
}

// An address the request cannot even be built for fails before any call.
func TestGitHubRejectsAnUnusableURL(t *testing.T) {
	source := githubSource{apiURL: "://", client: http.DefaultClient}

	if _, _, err := source.readme(context.Background(), testRef); err == nil {
		t.Error("readme returned nil for an unusable base url")
	}
}
