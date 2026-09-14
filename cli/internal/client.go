package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Client talks to one registry with one API token.
type Client struct {
	Registry string
	Token    string
	HTTP     *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		Registry: strings.TrimSuffix(cfg.Registry, "/"),
		Token:    cfg.Token,
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

// do sends the request and decodes into out. A non-2xx answer becomes an
// error built from the registry's JSON error body.
func (c *Client) do(method, path string, in, out any) error {
	var body *bytes.Buffer
	if in != nil {
		body = &bytes.Buffer{}
		if err := json.NewEncoder(body).Encode(in); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	} else {
		body = bytes.NewBuffer(nil)
	}

	req, err := http.NewRequest(method, c.Registry+path, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		var apiErr ErrorResponse
		if json.NewDecoder(res.Body).Decode(&apiErr) == nil && apiErr.Error != "" {
			return fmt.Errorf("%s%s", apiErr.Error, fieldLines(apiErr.Fields))
		}
		return fmt.Errorf("the registry answered %d", res.StatusCode)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// fieldLines appends the rejected fields to the registry's message. It stays
// on one line: every lpm error is one line on stderr, so a caller reading the
// output does not have to piece a message back together.
func fieldLines(fields map[string]string) string {
	if len(fields) == 0 {
		return ""
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%s: %s", name, fields[name])
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// Me identifies the token's account.
func (c *Client) Me() (PublicUser, error) {
	var user PublicUser
	err := c.do(http.MethodGet, "/auth/me", nil, &user)
	return user, err
}

func (c *Client) CreatePackage(pkg NewPackage) (Package, error) {
	var created Package
	err := c.do(http.MethodPost, "/packages", pkg, &created)
	return created, err
}

func (c *Client) CreateRelease(packageName string, rel NewRelease) (Release, error) {
	var created Release
	err := c.do(http.MethodPost, "/packages/"+url.PathEscape(packageName)+"/releases", rel, &created)
	return created, err
}

// GetPackage reads one package with every release it has published. This is
// what resolution runs on: one request per package name, not one per version.
func (c *Client) GetPackage(name string) (Package, error) {
	var pkg Package
	err := c.do(http.MethodGet, "/packages/"+url.PathEscape(name), nil, &pkg)
	return pkg, err
}

// SearchPackages lists packages matching the filter. An empty filter lists
// the newest.
func (c *Client) SearchPackages(filter PackageFilter) ([]Package, error) {
	query := url.Values{}
	for key, value := range map[string]string{
		"platform": filter.Platform,
		"name":     filter.Name,
		"q":        filter.Search,
		"username": filter.Username,
		"version":  filter.Version,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}

	path := "/packages"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var packages []Package
	err := c.do(http.MethodGet, path, nil, &packages)
	return packages, err
}
