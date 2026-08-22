package src

// Server holds the dependencies every handler needs, and owns the routing table.

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	db *pgxpool.Pool
}

func NewServer(db *pgxpool.Pool) *Server {
	return &Server{db: db}
}

// Routes pairs each path with a bare pattern. ServeMux prefers the
// method-qualified one, so the bare one catches method mismatches as JSON 405.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/", notFound) // least specific: catches everything unrouted

	mux.HandleFunc("GET /{$}", s.RootHandler)
	mux.HandleFunc("/{$}", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("GET /health", s.HealthCheckHandler)
	mux.HandleFunc("/health", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("GET /users/{username}", s.GetUserHandler)
	mux.HandleFunc("/users/{username}", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("POST /user/register", s.UserRegisterHandler)
	mux.HandleFunc("/user/register", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /user/login", s.UserLoginHandler)
	mux.HandleFunc("/user/login", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /user/change_password", s.UserChangePasswordHandler)
	mux.HandleFunc("/user/change_password", methodNotAllowed(http.MethodPost))

	return mux
}

// notFound answers unrouted paths in the API's JSON error shape.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "no route matches "+r.Method+" "+r.URL.Path)
}

// methodNotAllowed sets the Allow header RFC 9110 requires on a 405.
func methodNotAllowed(allowed ...string) http.HandlerFunc {
	// GET routes also serve HEAD; every route answers OPTIONS.
	advertised := append([]string{}, allowed...)
	for _, m := range allowed {
		if m == http.MethodGet {
			advertised = append(advertised, http.MethodHead)
		}
	}
	advertised = append(advertised, http.MethodOptions)
	allow := strings.Join(advertised, ", ")

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)

		// Allow header already answered it.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		writeError(w, r, http.StatusMethodNotAllowed,
			"method "+r.Method+" is not allowed on this path, use "+allow)
	}
}
