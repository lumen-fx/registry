package src

import (
	"context"
	"errors"
	"fmt"
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

const releaseColumns = `id, package_id, url, version, description, created_at`

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

	return &release, nil
}

func (s *Server) publishPackage(ctx context.Context, publisher User, packaged NewPackage) (*Package, error) {
	rows, err := s.db.Query(ctx,
		`INSERT INTO packages (publisher_id, platform, name, description) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING RETURNING `+packageColumns,
		publisher.ID,
		packaged.Platform,
		packaged.Name,
		packaged.Description,
	)
	if err != nil {
		return nil, fmt.Errorf("insert package: %w", err)
	}

	// A conflict returns no rows, so no rows means duplicate.
	createdPackage, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Package])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackageExists
	}
	if err != nil {
		return nil, fmt.Errorf("collect package: %w", err)
	}

	createdPackage.Releases = []Release{} // [] reads better than null
	return &createdPackage, nil
}

func (s *Server) publishRelease(ctx context.Context, publisher User, packaged Package, release NewRelease) (*Release, error) {
	if packaged.PublisherID != publisher.ID {
		return nil, ErrNotPublisher
	}

	rows, err := s.db.Query(ctx,
		`INSERT INTO releases (package_id, url, version, description)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT DO NOTHING
		 RETURNING `+releaseColumns,
		packaged.ID,
		release.URL,
		release.Version,
		release.Description,
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

	return &createdRelease, nil
}
