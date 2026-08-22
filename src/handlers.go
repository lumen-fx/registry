package src

// HTTP handlers. Each one decodes input, validates it, calls a store method,
// and hands the result to writeJSON / writeError.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

	user, err := s.verifyLogin(r.Context(), loginUser)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeError(w, r, http.StatusUnauthorized, "invalid username or password")
		return
	case err != nil:
		writeServerError(w, r, "verify login", err)
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
