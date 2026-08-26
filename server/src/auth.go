package src

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	sessionCookie = "lpm_session"
	stateCookie   = "lpm_oauth_state"
	sessionTTL    = 7 * 24 * time.Hour
	tokenPrefix   = "lpm_"
)

// Secrets are stored hashed, so neither table can be replayed if it leaks.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// Cloudflare and the ingress terminate TLS, so the scheme arrives forwarded.
func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge / time.Second),
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) createSession(ctx context.Context, userID uuid.UUID) (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx,
		`INSERT INTO sessions (id_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashSecret(secret), userID, time.Now().Add(sessionTTL)); err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return secret, nil
}

func (s *Server) sessionUser(ctx context.Context, secret string) (*User, error) {
	rows, err := s.db.Query(ctx,
		`SELECT u.id, u.username, u.github_id, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id_hash = $1 AND s.expires_at > now()`, hashSecret(secret))
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("collect session user: %w", err)
	}
	user.Packages = []Package{} // [] reads better than null
	return &user, nil
}

func (s *Server) deleteSession(ctx context.Context, secret string) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM sessions WHERE id_hash = $1`, hashSecret(secret)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Server) createToken(ctx context.Context, userID uuid.UUID, name string) (*CreatedToken, error) {
	secret, err := newSecret()
	if err != nil {
		return nil, err
	}
	secret = tokenPrefix + secret

	rows, err := s.db.Query(ctx,
		`INSERT INTO tokens (user_id, name, token_hash)
		 VALUES ($1, $2, $3)
		 RETURNING id, user_id, name, token_hash, created_at, last_used_at`,
		userID, name, hashSecret(secret))
	if err != nil {
		return nil, fmt.Errorf("insert token: %w", err)
	}

	token, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Token])
	if err != nil {
		return nil, fmt.Errorf("collect token: %w", err)
	}
	return &CreatedToken{Token: token, Secret: secret}, nil
}

func (s *Server) listTokens(ctx context.Context, userID uuid.UUID) ([]Token, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, user_id, name, token_hash, created_at, last_used_at
		 FROM tokens WHERE user_id = $1 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}

	tokens, err := pgx.CollectRows(rows, pgx.RowToStructByName[Token])
	if err != nil {
		return nil, fmt.Errorf("collect tokens: %w", err)
	}
	return tokens, nil
}

func (s *Server) revokeToken(ctx context.Context, userID, tokenID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM tokens WHERE id = $1 AND user_id = $2`, tokenID, userID)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// tokenUser resolves a bearer secret and stamps its use in the same query.
func (s *Server) tokenUser(ctx context.Context, secret string) (*User, error) {
	rows, err := s.db.Query(ctx,
		`UPDATE tokens t SET last_used_at = now()
		 FROM users u
		 WHERE t.token_hash = $1 AND u.id = t.user_id
		 RETURNING u.id, u.username, u.github_id, u.created_at`, hashSecret(secret))
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("collect token user: %w", err)
	}
	user.Packages = []Package{} // [] reads better than null
	return &user, nil
}

// Writes the 401 itself. Callers only check ok.
func (s *Server) cookieAuth(w http.ResponseWriter, r *http.Request) (*User, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "sign in first")
		return nil, false
	}

	user, err := s.sessionUser(r.Context(), cookie.Value)
	if errors.Is(err, ErrInvalidCredentials) {
		writeError(w, r, http.StatusUnauthorized, "sign in first")
		return nil, false
	}
	if err != nil {
		writeServerError(w, r, "session auth", err)
		return nil, false
	}
	return user, true
}

// Writes the 401 itself. Callers only check ok.
func (s *Server) bearerAuth(w http.ResponseWriter, r *http.Request) (*User, bool) {
	unauthorized := func() {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+serviceName+`"`)
		writeError(w, r, http.StatusUnauthorized, "a valid API token is required")
	}

	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || secret == "" {
		unauthorized()
		return nil, false
	}

	user, err := s.tokenUser(r.Context(), secret)
	if errors.Is(err, ErrInvalidCredentials) {
		unauthorized()
		return nil, false
	}
	if err != nil {
		writeServerError(w, r, "token auth", err)
		return nil, false
	}
	return user, true
}

func (s *Server) GitHubLoginHandler(w http.ResponseWriter, r *http.Request) {
	if !s.github.configured() {
		writeError(w, r, http.StatusServiceUnavailable, "GitHub sign-in is not configured")
		return
	}

	state, err := newSecret()
	if err != nil {
		writeServerError(w, r, "new state", err)
		return
	}
	setCookie(w, r, stateCookie, state, 10*time.Minute)

	redirect := s.github.authorizeURL + "?client_id=" + s.github.clientID + "&state=" + state
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (s *Server) GitHubCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if !s.github.configured() {
		writeError(w, r, http.StatusServiceUnavailable, "GitHub sign-in is not configured")
		return
	}

	cookie, err := r.Cookie(stateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		writeError(w, r, http.StatusForbidden, "the sign-in state does not match; start over")
		return
	}
	setCookie(w, r, stateCookie, "", -1)

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, r, http.StatusBadRequest, "the callback is missing its code")
		return
	}

	accessToken, err := s.github.exchange(r.Context(), code)
	if err != nil {
		writeServerError(w, r, "github exchange", err)
		return
	}
	githubID, login, err := s.github.user(r.Context(), accessToken)
	if err != nil {
		writeServerError(w, r, "github user", err)
		return
	}

	// GitHub logins already fit the username shape; this guards the contract.
	if len(login) > usernameMaxLen || !usernamePattern.MatchString(login) {
		writeError(w, r, http.StatusBadGateway, "GitHub returned an unusable login")
		return
	}

	user, err := s.upsertGitHubUser(r.Context(), githubID, login)
	if err != nil {
		writeServerError(w, r, "upsert user", err)
		return
	}

	session, err := s.createSession(r.Context(), user.ID)
	if err != nil {
		writeServerError(w, r, "create session", err)
		return
	}
	setCookie(w, r, sessionCookie, session, sessionTTL)

	LoggerFrom(r.Context()).Info("signed in", slog.String("username", user.Username))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if err := s.deleteSession(r.Context(), cookie.Value); err != nil {
			writeServerError(w, r, "delete session", err)
			return
		}
	}
	setCookie(w, r, sessionCookie, "", -1)
	writeJSON(w, r, http.StatusOK, StatusResponse{Status: "signed out"})
}

// The session cookie serves the UI; a bearer token serves the CLI.
func (s *Server) MeHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		user, ok := s.bearerAuth(w, r)
		if !ok {
			return
		}
		writeJSON(w, r, http.StatusOK, user.Public())
		return
	}

	user, ok := s.cookieAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, r, http.StatusOK, user.Public())
}

func (s *Server) ListTokensHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cookieAuth(w, r)
	if !ok {
		return
	}

	tokens, err := s.listTokens(r.Context(), user.ID)
	if err != nil {
		writeServerError(w, r, "list tokens", err)
		return
	}
	writeJSON(w, r, http.StatusOK, tokens)
}

func (s *Server) CreateTokenHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cookieAuth(w, r)
	if !ok {
		return
	}

	var newToken NewToken
	if !decodeJSON(w, r, &newToken) {
		return
	}
	if fields := newToken.Validate(); !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	token, err := s.createToken(r.Context(), user.ID, newToken.Name)
	if err != nil {
		writeServerError(w, r, "create token", err)
		return
	}
	writeJSON(w, r, http.StatusCreated, token)
}

func (s *Server) RevokeTokenHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cookieAuth(w, r)
	if !ok {
		return
	}

	tokenID, err := uuid.Parse(r.PathValue("token"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "token doesn't exist")
		return
	}

	err = s.revokeToken(r.Context(), user.ID, tokenID)
	switch {
	case errors.Is(err, ErrTokenNotFound):
		writeError(w, r, http.StatusNotFound, "token doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "revoke token", err)
		return
	}
	writeJSON(w, r, http.StatusOK, StatusResponse{Status: "token was revoked"})
}
