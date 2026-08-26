package src

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
	// Lets a client fix a whole form at once.
	Fields map[string]string `json:"fields,omitempty"`
}

type StatusResponse struct {
	Status string `json:"status"`
}

type User struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Username  string    `json:"username" db:"username"`
	GitHubID  int64     `json:"-" db:"github_id"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	Packages  []Package `json:"packages" db:"-"`
}

// PublicUser hides fields only the owner may see.
type PublicUser struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	Packages  []Package `json:"packages"`
}

func (u *User) Public() PublicUser {
	return PublicUser{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt, Packages: u.Packages}
}

// Token rows never expose the hash; the secret exists only in the create
// response.
type Token struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	UserID     uuid.UUID  `json:"-" db:"user_id"`
	Name       string     `json:"name" db:"name"`
	TokenHash  string     `json:"-" db:"token_hash"`
	CreatedAt  time.Time  `json:"createdAt" db:"created_at"`
	LastUsedAt *time.Time `json:"lastUsedAt" db:"last_used_at"`
}

type NewToken struct {
	Name string `json:"name"`
}

type CreatedToken struct {
	Token
	Secret string `json:"token"`
}

type Release struct {
	ID          uuid.UUID `json:"id" db:"id"`
	PackageID   uuid.UUID `json:"-" db:"package_id"`
	URL         string    `json:"url" db:"url"`
	Version     string    `json:"version" db:"version"`
	Description string    `json:"description" db:"description"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
}

// PackageFilter combines set fields with AND.
type PackageFilter struct {
	Platform string // exact match
	Name     string // case-insensitive substring of name
	Search   string // case-insensitive substring of name or description
	Username string // publisher's username
	Version  string // has a release with this version
	Limit    int    // clamped to packagesMaxLimit, <=0 means default
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

type NewPackage struct {
	Platform    string `json:"platform" db:"platform"`
	Name        string `json:"name" db:"name"`
	Description string `json:"description" db:"description"`
}

// Package comes from the path, publisher from credentials.
type NewRelease struct {
	URL         string `json:"url" db:"url"`
	Version     string `json:"version" db:"version"`
	Description string `json:"description" db:"description"`
}
