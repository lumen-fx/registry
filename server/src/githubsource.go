package src

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// The registry stores no bytes, so it holds neither the README a release was
// published from nor how often its archives have been downloaded. Both live on
// the publisher's GitHub release, and githubSource is what reads them. The
// base URL is configurable so the tests can stand in for GitHub, the same way
// githubOAuth is; production never sets it.
type githubSource struct {
	apiURL string
	token  string
	client *http.Client
}

var (
	// ErrNoReadme covers both "the repository has none" and "the release is
	// not on GitHub", because a reader can do nothing different about either.
	ErrNoReadme      = errors.New("no readme")
	ErrNoGitHubRef   = errors.New("release has no github artifact")
	ErrGitHubLimited = errors.New("github rate limit reached")
)

const (
	// A README is rendered in a browser. Past this size it is not a README,
	// and the registry is not a file host.
	readmeMaxBytes = 512 * 1024

	// Well under the request timeout, so a page view fails fast when GitHub
	// is slow rather than dying in the middleware.
	githubSourceTimeout = 3 * time.Second
)

func configGitHubSource() githubSource {
	return githubSource{
		apiURL: strings.TrimSuffix(envOr("GITHUB_API_URL", "https://api.github.com"), "/"),
		token:  os.Getenv("GITHUB_TOKEN"),
		client: &http.Client{Timeout: githubSourceTimeout},
	}
}

// releaseRef is a GitHub release: the repository that published it and the tag
// it was cut from. Both the README and the download counts hang off it.
type releaseRef struct {
	Owner string
	Repo  string
	Tag   string
}

func (r releaseRef) String() string { return r.Owner + "/" + r.Repo + "@" + r.Tag }

// parseReleaseURL reads a release download URL, the shape every GitHub release
// asset has:
//
//	https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>
//
// A tag may contain slashes, so everything between "download" and the asset
// name is the tag. Anything else, including a URL on another host, is not a
// GitHub release and yields false.
func parseReleaseURL(raw string) (ref releaseRef, asset string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return releaseRef{}, "", false
	}
	if host := strings.ToLower(u.Hostname()); host != "github.com" && host != "www.github.com" {
		return releaseRef{}, "", false
	}

	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	// owner, repo, "releases", "download", at least one tag segment, asset.
	if len(parts) < 6 || parts[2] != "releases" || parts[3] != "download" {
		return releaseRef{}, "", false
	}

	unescaped := make([]string, len(parts))
	for i, part := range parts {
		if part == "" {
			return releaseRef{}, "", false
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return releaseRef{}, "", false
		}
		unescaped[i] = decoded
	}

	ref = releaseRef{
		Owner: unescaped[0],
		Repo:  unescaped[1],
		Tag:   strings.Join(unescaped[4:len(unescaped)-1], "/"),
	}
	return ref, unescaped[len(unescaped)-1], true
}

// releaseRefFor finds the GitHub release a published release's artifacts point
// at. Artifacts arrive sorted by target, so a release whose artifacts somehow
// span two repositories resolves to the same one every time.
func releaseRefFor(release Release) (releaseRef, bool) {
	for _, artifact := range release.Artifacts {
		if ref, _, ok := parseReleaseURL(artifact.URL); ok {
			return ref, true
		}
	}
	return releaseRef{}, false
}

// get performs an authenticated API call. A token is optional: without one
// GitHub allows sixty requests an hour per address, which the cache survives
// and the collector does not.
func (g githubSource) get(ctx context.Context, endpoint string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiURL+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("github request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	// A spent rate limit is reported as a refusal, and retrying it costs
	// another request against a limit that is already empty.
	if (res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests) &&
		res.Header.Get("X-RateLimit-Remaining") == "0" {
		res.Body.Close()
		return nil, ErrGitHubLimited
	}

	return res, nil
}

// readme reads the repository's README as it stood at the release's tag, so a
// page shows what that version documented rather than what main says today.
// GitHub finds the file whatever it is called.
func (g githubSource) readme(ctx context.Context, ref releaseRef) (markdown string, sourceURL string, err error) {
	endpoint := fmt.Sprintf("/repos/%s/%s/readme?ref=%s",
		url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), url.QueryEscape(ref.Tag))

	res, err := g.get(ctx, endpoint, "application/vnd.github+json")
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()

	// A private, renamed, or deleted repository answers 404 the same way a
	// repository without a README does.
	if res.StatusCode == http.StatusNotFound {
		return "", "", ErrNoReadme
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github: readme for %s answered %d", ref, res.StatusCode)
	}

	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		HTMLURL  string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4*readmeMaxBytes)).Decode(&payload); err != nil {
		return "", "", fmt.Errorf("github: decode readme for %s: %w", ref, err)
	}

	// GitHub sends base64 up to a megabyte and an empty body above it. A file
	// that large, or one that is not text, is nothing a README panel can show.
	if payload.Encoding != "base64" || payload.Content == "" {
		return "", "", ErrNoReadme
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return "", "", fmt.Errorf("github: decode readme content for %s: %w", ref, err)
	}
	if len(decoded) > readmeMaxBytes || !utf8.Valid(decoded) {
		return "", "", ErrNoReadme
	}

	return string(decoded), payload.HTMLURL, nil
}

// assetDownloads reads how often each archive on the release has been
// downloaded, keyed by file name. The number counts every download of the
// asset, so CI runs and browsers are in it alongside installs.
func (g githubSource) assetDownloads(ctx context.Context, ref releaseRef) (map[string]int64, error) {
	endpoint := fmt.Sprintf("/repos/%s/%s/releases/tags/%s",
		url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), url.PathEscape(ref.Tag))

	res, err := g.get(ctx, endpoint, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNoGitHubRef
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: release %s answered %d", ref, res.StatusCode)
	}

	var payload struct {
		Assets []struct {
			Name          string `json:"name"`
			DownloadCount int64  `json:"download_count"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, readmeMaxBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("github: decode release %s: %w", ref, err)
	}

	counts := make(map[string]int64, len(payload.Assets))
	for _, asset := range payload.Assets {
		counts[asset.Name] = asset.DownloadCount
	}
	return counts, nil
}
