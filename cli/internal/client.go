package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
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

func fieldLines(fields map[string]string) string {
	if len(fields) == 0 {
		return ""
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "\n  %s: %s", name, fields[name])
	}
	return b.String()
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
