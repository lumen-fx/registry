package src

// Domain types and request/response payloads. `db` tags feed
// pgx.RowToStructByName; `json` tags define the wire format.

import (
	"time"

	"github.com/google/uuid"
)

type HealthCheck struct {
	Service  string `json:"service"`
	Status   string `json:"status"`
	Database string `json:"database"`
}

type ErrorResponse struct {
	Error string `json:"error"`
	// Fields carries per-field validation messages, so a client can fix a
	// whole form from one response instead of one field per round trip.
	Fields map[string]string `json:"fields,omitempty"`
}

type StatusResponse struct {
	Status string `json:"status"`
}

type User struct {
	ID           uuid.UUID `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
	Packages     []Package `json:"packages" db:"-"`
}

// PublicUser is the User fields safe to show to anyone. Register and login
// still return the full User: that is the caller's own record.
type PublicUser struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	Packages  []Package `json:"packages"`
}

func (u *User) Public() PublicUser {
	return PublicUser{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt, Packages: u.Packages}
}

type UserRegister struct {
	Username string `json:"username" db:"username"`
	Email    string `json:"email" db:"email"`
	Password string `json:"password" db:"password"`
}

type UserLogin struct {
	Username string `json:"username" db:"username"`
	Password string `json:"password" db:"password"`
}

type UserResetPassword struct {
	Username        string `json:"username" db:"username"`
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type Release struct {
	ID          uuid.UUID `json:"id" db:"id"`
	PackageID   uuid.UUID `json:"-" db:"package_id"`
	Version     string    `json:"version" db:"version"`
	Description string    `json:"description" db:"description"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
}

// PackageFilter selects packages. Zero fields mean no filter; set fields are
// combined with AND.
type PackageFilter struct {
	Platform string // exact match
	Name     string // case-insensitive substring of name
	Search   string // case-insensitive substring of name or description
	Username string // publisher's username
	Version  string // has a release with this version
	Limit    int    // clamped to packagesMaxLimit; <=0 means the default
}

type Package struct {
	ID          uuid.UUID   `json:"id" db:"id"`
	PublisherID uuid.UUID   `json:"-" db:"publisher_id"`
	Platform    string      `json:"platform" db:"platform"`
	Name        string      `json:"name" db:"name"`
	Description string      `json:"description" db:"description"`
	Releases    []Release   `json:"releases" db:"-"`
	Publisher   *PublicUser `json:"publisher,omitempty" db:"-"`
	CreatedAt   time.Time   `json:"createdAt" db:"created_at"`
}
