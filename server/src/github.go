package src

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// githubOAuth is the sign-in provider. The URLs are configurable so the tests
// can stand in for GitHub; production never sets them.
type githubOAuth struct {
	clientID     string
	clientSecret string
	authorizeURL string
	tokenURL     string
	userURL      string
	client       *http.Client
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func configGitHub() githubOAuth {
	return githubOAuth{
		clientID:     os.Getenv("GITHUB_CLIENT_ID"),
		clientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		authorizeURL: envOr("GITHUB_AUTHORIZE_URL", "https://github.com/login/oauth/authorize"),
		tokenURL:     envOr("GITHUB_TOKEN_URL", "https://github.com/login/oauth/access_token"),
		userURL:      envOr("GITHUB_USER_URL", "https://api.github.com/user"),
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (g githubOAuth) configured() bool {
	return g.clientID != "" && g.clientSecret != ""
}

// exchange trades the callback code for an access token.
func (g githubOAuth) exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange code: github answered %d", res.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("exchange code: github answered %q", payload.Error)
	}
	return payload.AccessToken, nil
}

// user fetches the signed-in account's identity.
func (g githubOAuth) user(ctx context.Context, accessToken string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.userURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	res, err := g.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("fetch github user: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("fetch github user: github answered %d", res.StatusCode)
	}

	var payload struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return 0, "", fmt.Errorf("decode github user: %w", err)
	}
	if payload.ID == 0 || payload.Login == "" {
		return 0, "", fmt.Errorf("github user response is missing id or login")
	}
	return payload.ID, payload.Login, nil
}
