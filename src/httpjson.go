package src

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const maxBodyBytes = 1 << 20 // 1 MiB

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	log := LoggerFrom(r.Context())

	body, err := json.Marshal(v)
	if err != nil {
		log.Error("encode response", slog.Any("error", err))
		// Written literally rather than through writeError: re-entering the
		// marshaller after it has already failed risks failing again.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
		return
	}

	// 204 and 304 must not carry a body. net/http discards one and logs a
	// complaint, so the caller's payload would vanish silently.
	if status == http.StatusNoContent || status == http.StatusNotModified {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		// Client hung up mid-write. Nothing to salvage, but worth knowing.
		log.Warn("write response", slog.Any("error", err))
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, ErrorResponse{Error: msg})
}

func writeFieldErrors(w http.ResponseWriter, r *http.Request, fields FieldErrors) {
	writeJSON(w, r, http.StatusUnprocessableEntity, ErrorResponse{
		Error:  "request contains invalid fields",
		Fields: fields,
	})
}

func writeServerError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	log := LoggerFrom(r.Context())

	if errors.Is(r.Context().Err(), context.Canceled) {
		log.Info("request cancelled by client", slog.String("op", msg))
		return
	}

	if errors.Is(err, context.DeadlineExceeded) {
		log.Error(msg, slog.Any("error", err))
		writeError(w, r, http.StatusServiceUnavailable, "service temporarily unavailable, try again")
		return
	}

	log.Error(msg, slog.Any("error", err))
	writeError(w, r, http.StatusInternalServerError, "internal server error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		var typeErr *json.UnmarshalTypeError
		var syntaxErr *json.SyntaxError

		switch {
		case errors.As(err, &maxErr):
			writeError(w, r, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body must not exceed %d bytes", maxErr.Limit))

		case errors.Is(err, io.EOF):
			writeError(w, r, http.StatusBadRequest, "request body is empty")

		case errors.Is(err, io.ErrUnexpectedEOF):
			writeError(w, r, http.StatusBadRequest, "malformed JSON: unexpected end of input")

		case errors.As(err, &typeErr):
			writeError(w, r, http.StatusBadRequest,
				fmt.Sprintf("field %q must be of type %s", typeErr.Field, typeErr.Type))

		case errors.As(err, &syntaxErr):
			writeError(w, r, http.StatusBadRequest,
				fmt.Sprintf("malformed JSON at byte %d", syntaxErr.Offset))

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			writeError(w, r, http.StatusBadRequest,
				"unrecognised field "+strings.TrimPrefix(err.Error(), "json: unknown field "))

		default:
			writeError(w, r, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}

	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "request body must contain a single JSON object")
		return false
	}

	return true
}
