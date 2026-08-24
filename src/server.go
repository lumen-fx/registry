package src

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

// Routes maps paths. Bare patterns give JSON 405s.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/", notFound) // catches everything unrouted

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

	mux.HandleFunc("GET /users/{username}/packages", s.UserPackagesHandler)
	mux.HandleFunc("/users/{username}/packages", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("GET /packages", s.SearchPackagesHandler)
	mux.HandleFunc("POST /packages", s.PublishPackageHandler)
	mux.HandleFunc("/packages", methodNotAllowed(http.MethodGet, http.MethodPost))

	mux.HandleFunc("GET /packages/{package}", s.GetPackageHandler)
	mux.HandleFunc("/packages/{package}", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("GET /packages/{package}/releases", s.PackageReleasesHandler)
	mux.HandleFunc("POST /packages/{package}/releases", s.PublishReleaseHandler)
	mux.HandleFunc("/packages/{package}/releases", methodNotAllowed(http.MethodGet, http.MethodPost))

	mux.HandleFunc("GET /packages/{package}/releases/{version}", s.GetReleaseHandler)
	mux.HandleFunc("/packages/{package}/releases/{version}", methodNotAllowed(http.MethodGet))

	return mux
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "no route matches "+r.Method+" "+r.URL.Path)
}

// Sets the Allow header a 405 requires.
func methodNotAllowed(allowed ...string) http.HandlerFunc {
	// GET routes also serve HEAD. All answer OPTIONS.
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
