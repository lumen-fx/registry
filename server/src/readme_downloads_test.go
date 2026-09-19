package src

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// githubStub stands in for the repository API the README and the download
// counts come from. A test says what GitHub holds and how it behaves; the
// stub counts calls, so a test can show the cache stopped one.
type githubStub struct {
	mu      sync.Mutex
	readmes map[string]string
	assets  map[string]map[string]int64
	down    bool
	limited bool
	calls   int
}

func stubKey(owner, repo, tag string) string { return owner + "/" + repo + "@" + tag }

func (g *githubStub) setReadme(owner, repo, tag, markdown string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.readmes == nil {
		g.readmes = map[string]string{}
	}
	g.readmes[stubKey(owner, repo, tag)] = markdown
}

func (g *githubStub) setDownloads(owner, repo, tag string, counts map[string]int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.assets == nil {
		g.assets = map[string]map[string]int64{}
	}
	g.assets[stubKey(owner, repo, tag)] = counts
}

// forgetReadmes is a publisher deleting the file the registry read.
func (g *githubStub) forgetReadmes() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.readmes = map[string]string{}
}

// setDown makes every call fail the way an outage does.
func (g *githubStub) setDown(down bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.down = down
}

// setLimited spends the rate limit, which GitHub reports as a refusal with
// nothing left on the counter.
func (g *githubStub) setLimited(limited bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limited = limited
}

func (g *githubStub) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func (g *githubStub) register(mux *http.ServeMux) {
	// A tag may contain slashes, so it is the rest of the path.
	mux.HandleFunc("GET /repos/{owner}/{repo}/readme", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.calls++
		if g.limited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if g.down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		owner, repo, tag := r.PathValue("owner"), r.PathValue("repo"), r.URL.Query().Get("ref")
		markdown, ok := g.readmes[stubKey(owner, repo, tag)]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"content":  base64.StdEncoding.EncodeToString([]byte(markdown)),
			"encoding": "base64",
			"html_url": fmt.Sprintf("https://github.com/%s/%s/blob/%s/README.md", owner, repo, tag),
		})
	})

	mux.HandleFunc("GET /repos/{owner}/{repo}/releases/tags/{tag...}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.calls++
		if g.limited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if g.down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		counts, ok := g.assets[stubKey(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("tag"))]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		assets := make([]map[string]any, 0, len(counts))
		for name, count := range counts {
			assets = append(assets, map[string]any{"name": name, "download_count": count})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": assets})
	})
}

// The registry's own fixtures host their archives on example.test, which is
// what a release hosted outside GitHub looks like. This one is on a release.
func (a *api) releaseOnGitHub(token, name, version, owner, repo, tag, asset string) Release {
	a.t.Helper()

	body := fmt.Sprintf(
		`{"version":%q,"artifacts":[{"target":"any","url":"https://github.com/%s/%s/releases/download/%s/%s","sha256":%q,"size":1024}]}`,
		version, owner, repo, tag, asset, testSHA256)
	res := a.expect(http.StatusCreated, http.MethodPost, "/packages/"+name+"/releases", body, token)

	var release Release
	res.json(a.t, &release)
	return release
}

// ageReadme moves a cached README's last attempt into the past, which is how a
// test reaches the refresh path without waiting a day for it.
func (a *api) ageReadme(by time.Duration) {
	a.t.Helper()
	_, err := testPool.Exec(context.Background(),
		`UPDATE release_readmes SET checked_at = checked_at - $1::interval`,
		fmt.Sprintf("%d seconds", int(by.Seconds())))
	if err != nil {
		a.t.Fatalf("age readme: %v", err)
	}
}

func TestE2EReadmeComesFromTheReleasesRepository(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.github.setReadme("lumen-fx", "strutil", "v1.0.0", "# strutil\n\nString helpers.")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")
	var readme Readme
	res.json(t, &readme)

	if readme.Markdown != "# strutil\n\nString helpers." {
		t.Errorf("markdown = %q", readme.Markdown)
	}
	if readme.Version != "1.0.0" {
		t.Errorf("version = %q, want the newest release", readme.Version)
	}
	if readme.Source != "https://github.com/lumen-fx/strutil/blob/v1.0.0/README.md" {
		t.Errorf("source = %q", readme.Source)
	}
	if readme.Stale {
		t.Error("a README just read is stale")
	}

	// The second request is served from the cache, so GitHub sees one call.
	before := a.github.callCount()
	a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")
	if after := a.github.callCount(); after != before {
		t.Errorf("github calls = %d, want the cached copy to be served without one", after-before)
	}
}

func TestE2EReadmeIsAbsentWhenThereIsNothingToRead(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")

	a.expect(http.StatusNotFound, http.MethodGet, "/packages/nothing/readme", "")

	// Claimed but never released: nothing names a repository yet.
	a.publish(owner.Token, "empty")
	a.expect(http.StatusNotFound, http.MethodGet, "/packages/empty/readme", "")

	// Released, but the archives are hosted outside GitHub.
	a.publish(owner.Token, "elsewhere")
	a.release(owner.Token, "elsewhere", "1.0.0")
	before := a.github.callCount()
	a.expect(http.StatusNotFound, http.MethodGet, "/packages/elsewhere/readme", "")
	if after := a.github.callCount(); after != before {
		t.Errorf("github was called %d times for a release it does not host", after-before)
	}

	// On GitHub, but the repository documents nothing.
	a.publish(owner.Token, "undocumented")
	a.releaseOnGitHub(owner.Token, "undocumented", "1.0.0", "o", "r", "v1.0.0", "pkg.tgz")
	a.expect(http.StatusNotFound, http.MethodGet, "/packages/undocumented/readme", "")
}

// An outage must not blank a page that had a README a minute ago.
func TestE2EReadmeSurvivesAnOutage(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.github.setReadme("lumen-fx", "strutil", "v1.0.0", "# strutil")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")
	a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")

	a.github.setDown(true)
	a.ageReadme(48 * time.Hour)

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")
	var readme Readme
	res.json(t, &readme)
	if readme.Markdown != "# strutil" {
		t.Errorf("markdown = %q, want the copy from before the outage", readme.Markdown)
	}
	if !readme.Stale {
		t.Error("a README served through an outage is not marked stale")
	}

	// The failure is cached too, so the next reader does not wait on GitHub
	// again.
	before := a.github.callCount()
	a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")
	if after := a.github.callCount(); after != before {
		t.Errorf("github calls = %d, want the failure to be remembered", after-before)
	}
}

// A README the publisher removes stops being shown.
func TestE2EReadmeDisappearsWithItsSource(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.github.setReadme("lumen-fx", "strutil", "v1.0.0", "# strutil")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")
	a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")

	a.github.forgetReadmes()
	a.ageReadme(48 * time.Hour)

	a.expect(http.StatusNotFound, http.MethodGet, "/packages/strutil/readme", "")
}

func TestE2EDownloadsAreEmptyUntilSampled(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/downloads", "")
	var downloads Downloads
	res.json(t, &downloads)

	if downloads.Total != 0 || len(downloads.Days) != 0 {
		t.Errorf("downloads = %+v, want nothing before the first sample", downloads)
	}
	if downloads.SampledAt != nil {
		t.Errorf("sampledAt = %v, want none", downloads.SampledAt)
	}
	a.expect(http.StatusNotFound, http.MethodGet, "/packages/nothing/downloads", "")
}

// GitHub reports a running total, so a day's downloads are the rise between
// two samples: the first sample sets the total and draws no bar, the second
// draws the difference.
func TestE2EDownloadsAreSampledDaily(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.github.setReadme("lumen-fx", "strutil", "v1.0.0", "# strutil")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")
	a.github.setDownloads("lumen-fx", "strutil", "v1.0.0", map[string]int64{"strutil.tgz": 10})

	ctx := context.Background()
	if err := Collect(ctx, discardLogger(), testPool); err != nil {
		t.Fatalf("collect: %v", err)
	}

	res := a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/downloads", "")
	var first Downloads
	res.json(t, &first)
	if first.Total != 10 {
		t.Errorf("total = %d, want the whole counter", first.Total)
	}
	if len(first.Days) != 0 {
		t.Errorf("days = %+v, want none from a single sample", first.Days)
	}
	if first.SampledAt == nil {
		t.Error("sampledAt is missing after a sample")
	}

	// Yesterday's sample, so today's has something to be measured against.
	if _, err := testPool.Exec(ctx,
		`UPDATE download_snapshots SET day = day - 1`); err != nil {
		t.Fatalf("age snapshot: %v", err)
	}

	a.github.setDownloads("lumen-fx", "strutil", "v1.0.0", map[string]int64{"strutil.tgz": 25})
	if err := Collect(ctx, discardLogger(), testPool); err != nil {
		t.Fatalf("collect: %v", err)
	}

	res = a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/downloads", "")
	var second Downloads
	res.json(t, &second)
	if second.Total != 25 {
		t.Errorf("total = %d, want the newest counter", second.Total)
	}
	if len(second.Days) != 1 || second.Days[0].Count != 15 {
		t.Fatalf("days = %+v, want one day of 15", second.Days)
	}
	if want := time.Now().UTC().Format(time.DateOnly); second.Days[0].Day != want {
		t.Errorf("day = %q, want %q", second.Days[0].Day, want)
	}
}

// The collector refreshes what a package page shows, so the page itself
// rarely waits on GitHub.
func TestE2ECollectRefreshesTheReadme(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.github.setReadme("lumen-fx", "strutil", "v1.0.0", "# strutil")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")
	a.github.setDownloads("lumen-fx", "strutil", "v1.0.0", map[string]int64{"strutil.tgz": 3})

	if err := Collect(context.Background(), discardLogger(), testPool); err != nil {
		t.Fatalf("collect: %v", err)
	}

	before := a.github.callCount()
	res := a.expect(http.StatusOK, http.MethodGet, "/packages/strutil/readme", "")
	if after := a.github.callCount(); after != before {
		t.Errorf("github calls = %d, want the collected copy to be served", after-before)
	}

	var readme Readme
	res.json(t, &readme)
	if readme.Markdown != "# strutil" {
		t.Errorf("markdown = %q", readme.Markdown)
	}
}

// A spent rate limit stops the pass: every call left would fail the same way,
// and what was already sampled stays.
func TestE2ECollectStopsOnASpentRateLimit(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")
	a.publish(owner.Token, "strutil")
	a.releaseOnGitHub(owner.Token, "strutil", "1.0.0", "lumen-fx", "strutil", "v1.0.0", "strutil.tgz")
	a.github.setLimited(true)

	err := Collect(context.Background(), discardLogger(), testPool)
	if !errors.Is(err, ErrGitHubLimited) {
		t.Fatalf("collect = %v, want the rate limit", err)
	}
}

// A release the publisher rewrote no longer carries the file the registry
// points at. The pass keeps going and the old samples stand; a new one would
// be a guess.
func TestE2ECollectSkipsWhatItCannotSample(t *testing.T) {
	a := newAPI(t)
	owner := a.signup("publisher")

	// Hosted outside GitHub: nothing to ask about.
	a.publish(owner.Token, "elsewhere")
	a.release(owner.Token, "elsewhere", "1.0.0")

	// On GitHub, but the tag is gone.
	a.publish(owner.Token, "retagged")
	a.releaseOnGitHub(owner.Token, "retagged", "1.0.0", "o", "r", "v1.0.0", "pkg.tgz")

	// On GitHub, but under a different file name than the registry recorded.
	a.publish(owner.Token, "renamed")
	a.releaseOnGitHub(owner.Token, "renamed", "1.0.0", "o", "renamed", "v1.0.0", "old.tgz")
	a.github.setDownloads("o", "renamed", "v1.0.0", map[string]int64{"new.tgz": 40})

	if err := Collect(context.Background(), discardLogger(), testPool); err != nil {
		t.Fatalf("collect: %v", err)
	}

	for _, name := range []string{"elsewhere", "retagged", "renamed"} {
		res := a.expect(http.StatusOK, http.MethodGet, "/packages/"+name+"/downloads", "")
		var downloads Downloads
		res.json(t, &downloads)
		if downloads.Total != 0 {
			t.Errorf("%s: total = %d, want nothing sampled", name, downloads.Total)
		}
	}
}
