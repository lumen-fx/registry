package src

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lumen-fx/registry/server/web"
)

const serviceName = "lumen-packages"

// Serves the browser UI. Static and database-free, so it doubles as the
// liveness endpoint the deployment probes.
func (s *Server) RootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(web.IndexHTML)
}

// Serves the CLI installer, so `curl registry/install.sh | sh` works with
// nothing but the registry's own hostname.
func (s *Server) InstallScriptHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(web.InstallScript)
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

func (s *Server) PublishPackageHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := s.bearerAuth(w, r)
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
	user, ok := s.bearerAuth(w, r)
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
