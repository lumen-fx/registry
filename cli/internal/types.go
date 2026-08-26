package internal

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
	ID           uuid.UUID `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
	Packages     []Package `json:"packages" db:"-"`
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

type GitHubRelease struct {
	ID              int64  `json:"id"`
	NodeID          string `json:"node_id"`
	HTMLURL         string `json:"html_url"`
	URL             string `json:"url"`
	TagName         string `json:"tag_name"`
	Name            string `json:"name"`
	TargetCommitish string `json:"target_commitish"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
	Body            string `json:"body"`

	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`

	Author GitHubUser `json:"author"`

	Assets        []GitHubReleaseAsset `json:"assets"`
	TarballURL    string               `json:"tarball_url"`
	ZipballURL    string               `json:"zipball_url"`
	AssetsURL     string               `json:"assets_url"`
	DiscussionURL string               `json:"discussion_url,omitempty"`
}

type GitHubReleaseAsset struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
	URL    string `json:"url"`

	Name  string `json:"name"`
	Label string `json:"label"`

	BrowserDownloadURL string `json:"browser_download_url"`

	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`

	State string `json:"state"`

	Digest string `json:"digest"`

	DownloadCount int64      `json:"download_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Uploader      GitHubUser `json:"uploader"`
}

type GitHubUser struct {
	Login   string `json:"login"`
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	HTMLURL string `json:"html_url"`
}
