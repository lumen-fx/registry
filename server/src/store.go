package src

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const userColumns = `id, username, github_id, created_at`

var ErrUserExists = errors.New("user already exists")
var ErrUserNotFound = errors.New("user not found")
var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrTokenNotFound = errors.New("token not found")
var ErrPackageNotFound = errors.New("package not found")
var ErrReleaseNotFound = errors.New("release not found")
var ErrPackageExists = errors.New("package already exists")
var ErrReleaseExists = errors.New("release already exists")
var ErrNotPublisher = errors.New("not the package publisher")
var ErrPackageHasReleases = errors.New("package has releases")

// upsertGitHubUser creates the account on first sign-in and follows GitHub
// renames afterwards; github_id is the identity, username the display name.
func (s *Server) upsertGitHubUser(ctx context.Context, githubID int64, username string) (*User, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO users (github_id, username)
		 VALUES ($1, $2)
		 ON CONFLICT (github_id) DO UPDATE SET username = EXCLUDED.username
		 RETURNING `+userColumns,
		githubID, username)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if err != nil {
		return nil, fmt.Errorf("collect user: %w", err)
	}
	user.Packages = []Package{} // [] reads better than null
	return &user, nil
}

// Skips the profile queries callers may not need.
func (s *Server) getUserRow(ctx context.Context, username string) (*User, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("collect user: %w", err)
	}

	return &user, nil
}

func (s *Server) getUser(ctx context.Context, username string) (*User, error) {
	user, err := s.getUserRow(ctx, username)
	if err != nil {
		return nil, err
	}

	// Capped so one profile lookup stays bounded.
	user.Packages, err = s.listPackages(ctx, PackageFilter{
		Username: user.Username,
		Limit:    packagesMaxLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list user packages: %w", err)
	}

	return user, nil
}

const packageColumns = `id, publisher_id, platform, name, description, created_at`

const (
	packagesDefaultLimit = 50
	packagesMaxLimit     = 200
)

// Stops user input acting as LIKE wildcards.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}

func (s *Server) listPackages(ctx context.Context, f PackageFilter) ([]Package, error) {
	var conds []string
	var args []any

	// Binds one arg and numbers its placeholder.
	add := func(format string, arg any) {
		args = append(args, arg)
		conds = append(conds, fmt.Sprintf(format, len(args)))
	}

	if f.Platform != "" {
		add(`platform = $%d`, f.Platform)
	}
	if f.Name != "" {
		add(`name ILIKE $%d`, "%"+escapeLike(f.Name)+"%")
	}
	if f.Search != "" {
		// One arg, referenced twice.
		args = append(args, "%"+escapeLike(f.Search)+"%")
		n := len(args)
		conds = append(conds, fmt.Sprintf(`(name ILIKE $%d OR description ILIKE $%d)`, n, n))
	}
	if f.Username != "" {
		add(`EXISTS (SELECT 1 FROM users u WHERE u.id = packages.publisher_id AND u.username = $%d)`, f.Username)
	}
	if f.Version != "" {
		add(`EXISTS (SELECT 1 FROM releases r WHERE r.package_id = packages.id AND r.version = $%d)`, f.Version)
	}

	statement := `SELECT ` + packageColumns + ` FROM packages`
	if len(conds) > 0 {
		statement += ` WHERE ` + strings.Join(conds, ` AND `)
	}

	// Unfiltered browsing wants newest first, a search wants by name.
	if len(conds) == 0 {
		statement += ` ORDER BY created_at DESC, id`
	} else {
		statement += ` ORDER BY name, id`
	}

	limit := f.Limit
	if limit <= 0 {
		limit = packagesDefaultLimit
	}
	limit = min(limit, packagesMaxLimit)

	args = append(args, limit)
	statement += fmt.Sprintf(` LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}

	// Never ErrNoRows. Dropping err would hide every failure.
	packages, err := pgx.CollectRows(rows, pgx.RowToStructByName[Package])
	if err != nil {
		return nil, fmt.Errorf("collect packages: %w", err)
	}

	if err := s.attachReleases(ctx, packages); err != nil {
		return nil, err
	}

	return packages, nil
}

const releaseColumns = `id, package_id, version, description, dependencies, requires, created_at`

const artifactColumns = `id, release_id, target, url, sha256, size`

// attachArtifacts fills in the artifacts of every release in one query, not
// one per release. It writes through the slice.
func (s *Server) attachArtifacts(ctx context.Context, releases []Release) error {
	if len(releases) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, len(releases))
	byRelease := make(map[uuid.UUID]*Release, len(releases))
	for i := range releases {
		ids[i] = releases[i].ID
		// [] reads better than null.
		releases[i].Artifacts = []Artifact{}
		byRelease[releases[i].ID] = &releases[i]
	}

	rows, err := s.db.Query(ctx,
		`SELECT `+artifactColumns+`
		 FROM artifacts
		 WHERE release_id = ANY($1)
		 ORDER BY release_id, target`, ids)
	if err != nil {
		return fmt.Errorf("list artifacts: %w", err)
	}

	artifacts, err := pgx.CollectRows(rows, pgx.RowToStructByName[Artifact])
	if err != nil {
		return fmt.Errorf("collect artifacts: %w", err)
	}

	for _, artifact := range artifacts {
		if rel, ok := byRelease[artifact.ReleaseID]; ok {
			rel.Artifacts = append(rel.Artifacts, artifact)
		}
	}

	return nil
}

// One query for all packages, not N+1. Newest first.
func (s *Server) attachReleases(ctx context.Context, packages []Package) error {
	if len(packages) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, len(packages))
	byPackage := make(map[uuid.UUID]*Package, len(packages))
	for i := range packages {
		ids[i] = packages[i].ID
		// [] reads better than null.
		packages[i].Releases = []Release{}
		byPackage[packages[i].ID] = &packages[i]
	}

	rows, err := s.db.Query(ctx,
		`SELECT `+releaseColumns+`
		 FROM releases
		 WHERE package_id = ANY($1)
		 ORDER BY package_id, created_at DESC, id`, ids)
	if err != nil {
		return fmt.Errorf("list releases: %w", err)
	}

	releases, err := pgx.CollectRows(rows, pgx.RowToStructByName[Release])
	if err != nil {
		return fmt.Errorf("collect releases: %w", err)
	}

	// Before the copies land in their packages, so every copy carries them.
	if err := s.attachArtifacts(ctx, releases); err != nil {
		return err
	}

	for _, rel := range releases {
		if p, ok := byPackage[rel.PackageID]; ok {
			p.Releases = append(p.Releases, rel)
		}
	}

	return nil
}

// Skips the releases query callers may not need.
func (s *Server) getPackageRow(ctx context.Context, name string) (*Package, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+packageColumns+` FROM packages WHERE name = $1`, name)
	if err != nil {
		return nil, fmt.Errorf("get package: %w", err)
	}

	packaged, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Package])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("collect package: %w", err)
	}

	return &packaged, nil
}

func (s *Server) getPackage(ctx context.Context, name string) (*Package, error) {
	packaged, err := s.getPackageRow(ctx, name)
	if err != nil {
		return nil, err
	}

	// attachReleases writes through the slice.
	packages := []Package{*packaged}
	if err := s.attachReleases(ctx, packages); err != nil {
		return nil, err
	}

	return &packages[0], nil
}

func (s *Server) getRelease(ctx context.Context, name string, version string) (*Release, error) {
	packaged, err := s.getPackageRow(ctx, name)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT `+releaseColumns+`
		 FROM releases
		 WHERE package_id = $1 AND version = $2`, packaged.ID, version)
	if err != nil {
		return nil, fmt.Errorf("get release: %w", err)
	}

	release, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Release])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("collect release: %w", err)
	}

	// attachArtifacts writes through the slice.
	releases := []Release{release}
	if err := s.attachArtifacts(ctx, releases); err != nil {
		return nil, err
	}

	return &releases[0], nil
}

// publishPackage claims a name for its publisher. The publisher who holds a
// name can claim it again to change its description: a manifest-driven release
// claims on every run with whatever the manifest says now, so the second claim
// is how an edited description reaches the registry. Anyone else finds the
// name taken. The platform is part of the claim and does not change.
func (s *Server) publishPackage(ctx context.Context, publisher User, packaged NewPackage) (*Package, bool, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO packages (publisher_id, platform, name, description) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
		 WHERE packages.publisher_id = EXCLUDED.publisher_id AND packages.platform = EXCLUDED.platform
		 RETURNING `+packageColumns+`, (xmax = 0) AS created`,
		publisher.ID,
		packaged.Platform,
		packaged.Name,
		packaged.Description,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert package: %w", err)
	}

	// A conflict the WHERE clause rejects returns no rows, so no rows means
	// the name belongs to someone else or to another platform.
	claimed, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[claimedPackage])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrPackageExists
	}
	if err != nil {
		return nil, false, fmt.Errorf("collect package: %w", err)
	}

	claimed.Releases = []Release{} // [] reads better than null
	return &claimed.Package, claimed.Created, nil
}

// claimedPackage is a package row plus whether the claim inserted it. xmax is
// zero on a row the statement inserted and set on one it updated.
type claimedPackage struct {
	Package
	Created bool `db:"created"`
}

// deletePackage frees a name its publisher claimed and never released to. The
// release check rides inside the delete, so a release published alongside it
// keeps the package: the statement then matches no row.
func (s *Server) deletePackage(ctx context.Context, publisher User, packaged Package) error {
	if packaged.PublisherID != publisher.ID {
		return ErrNotPublisher
	}

	tag, err := s.db.Exec(ctx,
		`DELETE FROM packages
		 WHERE id = $1
		   AND NOT EXISTS (SELECT 1 FROM releases WHERE package_id = packages.id)`, packaged.ID)
	if err != nil {
		return fmt.Errorf("delete package: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPackageHasReleases
	}
	return nil
}

// publishRelease writes the release and its artifacts together, so a release
// nobody can download never reaches the database.
func (s *Server) publishRelease(ctx context.Context, publisher User, packaged Package, release NewRelease) (*Release, error) {
	if packaged.PublisherID != publisher.ID {
		return nil, ErrNotPublisher
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a committed tx rolls back to nothing

	rows, err := tx.Query(ctx,
		`INSERT INTO releases (package_id, version, description, dependencies, requires)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT DO NOTHING
		 RETURNING `+releaseColumns,
		packaged.ID,
		release.Version,
		release.Description,
		release.Dependencies,
		release.Requires,
	)
	if err != nil {
		return nil, fmt.Errorf("insert release: %w", err)
	}

	createdRelease, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Release])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReleaseExists
	}
	if err != nil {
		return nil, fmt.Errorf("collect release: %w", err)
	}

	createdRelease.Artifacts = []Artifact{}
	for _, artifact := range release.Artifacts {
		rows, err := tx.Query(ctx,
			`INSERT INTO artifacts (release_id, target, url, sha256, size)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING `+artifactColumns,
			createdRelease.ID, artifact.Target, artifact.URL, artifact.SHA256, artifact.Size)
		if err != nil {
			return nil, fmt.Errorf("insert artifact: %w", err)
		}

		created, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Artifact])
		if err != nil {
			return nil, fmt.Errorf("collect artifact: %w", err)
		}
		createdRelease.Artifacts = append(createdRelease.Artifacts, created)
	}

	// Sorted, so a response looks the same however the request was ordered.
	slices.SortFunc(createdRelease.Artifacts, func(a, b Artifact) int {
		return strings.Compare(a.Target, b.Target)
	})

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit release: %w", err)
	}

	return &createdRelease, nil
}
