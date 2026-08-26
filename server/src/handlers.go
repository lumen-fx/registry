package src

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const serviceName = "lumen-packages"

func (s *Server) RootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Hello, World!")
}

func (s *Server) HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		LoggerFrom(r.Context()).Error("health check: ping database", slog.Any("error", err))
		writeJSON(w, r, http.StatusServiceUnavailable, HealthCheck{
			Service:  serviceName,
			Status:   "unavailable",
			Database: "down",
		})
		return
	}

	writeJSON(w, r, http.StatusOK, HealthCheck{
		Service:  serviceName,
		Status:   "OK",
		Database: "up",
	})
}

func (s *Server) GetUserHandler(w http.ResponseWriter, r *http.Request) {
	user, err := s.getUser(r.Context(), r.PathValue("username"))
	switch {
	case errors.Is(err, ErrUserNotFound):
		writeError(w, r, http.StatusNotFound, "username doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "get user", err)
		return
	}

	writeJSON(w, r, http.StatusOK, user.Public())
}

func (s *Server) UserRegisterHandler(w http.ResponseWriter, r *http.Request) {
	var newUser UserRegister
	if !decodeJSON(w, r, &newUser) {
		return
	}
	if fields := newUser.Validate(); !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	user, err := s.createUser(r.Context(), newUser)
	switch {
	case errors.Is(err, ErrUserExists):
		writeError(w, r, http.StatusConflict, "username or email is already taken")
		return
	case err != nil:
		writeServerError(w, r, "create user", err)
		return
	}

	writeJSON(w, r, http.StatusCreated, user)
}

func (s *Server) UserLoginHandler(w http.ResponseWriter, r *http.Request) {
	var loginUser UserLogin
	if !decodeJSON(w, r, &loginUser) {
		return
	}
	if fields := loginUser.Validate(); !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	authed, err := s.verifyLogin(r.Context(), loginUser)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeError(w, r, http.StatusUnauthorized, "invalid username or password")
		return
	case err != nil:
		writeServerError(w, r, "verify login", err)
		return
	}

	// Load the profile after auth so timing leaks nothing.
	user, err := s.getUser(r.Context(), authed.Username)
	if err != nil {
		writeServerError(w, r, "get user", err)
		return
	}

	writeJSON(w, r, http.StatusOK, user)
}

func (s *Server) UserChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	var resetUser UserResetPassword
	if !decodeJSON(w, r, &resetUser) {
		return
	}
	if fields := resetUser.Validate(); !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	err := s.changePassword(r.Context(), resetUser)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeError(w, r, http.StatusUnauthorized, "invalid username or password")
		return
	case errors.Is(err, ErrUserNotFound):
		writeError(w, r, http.StatusNotFound, "username doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "change password", err)
		return
	}

	writeJSON(w, r, http.StatusOK, StatusResponse{Status: "password was changed"})
}

func (s *Server) UserPackagesHandler(w http.ResponseWriter, r *http.Request) {
	user, err := s.getUser(r.Context(), r.PathValue("username"))
	switch {
	case errors.Is(err, ErrUserNotFound):
		writeError(w, r, http.StatusNotFound, "username doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "get user", err)
		return
	}

	writeJSON(w, r, http.StatusOK, user.Packages)
}

func (s *Server) PackageReleasesHandler(w http.ResponseWriter, r *http.Request) {
	packaged, err := s.getPackage(r.Context(), r.PathValue("package"))
	switch {
	case errors.Is(err, ErrPackageNotFound):
		writeError(w, r, http.StatusNotFound, "package doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "get package", err)
		return
	}

	writeJSON(w, r, http.StatusOK, packaged.Releases)
}

// A missing package is a different problem from a missing version.
func (s *Server) GetReleaseHandler(w http.ResponseWriter, r *http.Request) {
	release, err := s.getRelease(r.Context(), r.PathValue("package"), r.PathValue("version"))
	switch {
	case errors.Is(err, ErrPackageNotFound):
		writeError(w, r, http.StatusNotFound, "package doesn't exist")
		return
	case errors.Is(err, ErrReleaseNotFound):
		writeError(w, r, http.StatusNotFound, "version isn't published")
		return
	case err != nil:
		writeServerError(w, r, "get release", err)
		return
	}

	writeJSON(w, r, http.StatusOK, release)
}

func (s *Server) SearchPackagesHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	filter := PackageFilter{
		Platform: query.Get("platform"),
		Name:     query.Get("name"),
		Search:   query.Get("q"),
		Username: query.Get("username"),
		Version:  query.Get("version"),
	}
	fields := filter.Validate()

	// Unparseable limit is a client error, not a default.
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			fields.add("limit", "must be a whole number")
		case limit < 1:
			fields.add("limit", "must be at least 1")
		default:
			filter.Limit = limit // listPackages clamps this to packagesMaxLimit
		}
	}

	if !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	packages, err := s.listPackages(r.Context(), filter)
	if err != nil {
		writeServerError(w, r, "list packages", err)
		return
	}

	writeJSON(w, r, http.StatusOK, packages)
}

func (s *Server) GetPackageHandler(w http.ResponseWriter, r *http.Request) {
	packaged, err := s.getPackage(r.Context(), r.PathValue("package"))
	switch {
	case errors.Is(err, ErrPackageNotFound):
		writeError(w, r, http.StatusNotFound, "package doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "get package", err)
		return
	}

	writeJSON(w, r, http.StatusOK, packaged)
}

// Writes the 401 itself. Callers only check ok.
func (s *Server) basicAuth(w http.ResponseWriter, r *http.Request) (*User, bool) {
	// RFC 9110 requires a challenge on every 401.
	unauthorized := func() {
		w.Header().Set("WWW-Authenticate", `Basic realm="`+serviceName+`", charset="UTF-8"`)
		writeError(w, r, http.StatusUnauthorized, "valid credentials are required")
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		unauthorized()
		return nil, false
	}

	user, err := s.verifyLogin(r.Context(), UserLogin{Username: username, Password: password})
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		unauthorized()
		return nil, false
	case err != nil:
		writeServerError(w, r, "verify login", err)
		return nil, false
	}

	return user, true
}

func (s *Server) PublishPackageHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.basicAuth(w, r)
	if !ok {
		return
	}

	var newPackage NewPackage
	if !decodeJSON(w, r, &newPackage) {
		return
	}
	if fields := newPackage.Validate(); !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	packaged, err := s.publishPackage(r.Context(), *user, newPackage)
	switch {
	case errors.Is(err, ErrPackageExists):
		writeError(w, r, http.StatusConflict, "package name is already taken")
		return
	case err != nil:
		writeServerError(w, r, "publish package", err)
		return
	}

	writeJSON(w, r, http.StatusCreated, packaged)
}

func (s *Server) PublishReleaseHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.basicAuth(w, r)
	if !ok {
		return
	}

	var newRelease NewRelease
	if !decodeJSON(w, r, &newRelease) {
		return
	}
	if fields := newRelease.Validate(); !fields.ok() {
		writeFieldErrors(w, r, fields)
		return
	}

	packaged, err := s.getPackageRow(r.Context(), r.PathValue("package"))
	switch {
	case errors.Is(err, ErrPackageNotFound):
		writeError(w, r, http.StatusNotFound, "package doesn't exist")
		return
	case err != nil:
		writeServerError(w, r, "get package", err)
		return
	}

	release, err := s.publishRelease(r.Context(), *user, *packaged, newRelease)
	switch {
	case errors.Is(err, ErrNotPublisher):
		writeError(w, r, http.StatusForbidden, "only the package publisher may publish its releases")
		return
	case errors.Is(err, ErrReleaseExists):
		writeError(w, r, http.StatusConflict, "release version is already taken")
		return
	case err != nil:
		writeServerError(w, r, "publish release", err)
		return
	}

	writeJSON(w, r, http.StatusCreated, release)
}
