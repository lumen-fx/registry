package src

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const userColumns = `id, username, email, password_hash, created_at`

var ErrUserExists = errors.New("user already exists")
var ErrUserNotFound = errors.New("user not found")
var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrPackageNotFound = errors.New("package not found")
var ErrReleaseNotFound = errors.New("release not found")
var ErrPackageExists = errors.New("package already exists")
var ErrReleaseExists = errors.New("release already exists")
var ErrNotPublisher = errors.New("not the package publisher")

func (s *Server) createUser(ctx context.Context, u UserRegister) (*User, error) {
	hash, err := argon2id.CreateHash(u.Password, argon2id.DefaultParams)
	if err != nil {
		return nil, fmt.Errorf("create hash: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`INSERT INTO users (email, username, password_hash)
		 VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING
		 RETURNING `+userColumns,
		u.Email, u.Username, hash)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserExists
	}
	if err != nil {
		return nil, fmt.Errorf("collect user: %w", err)
	}
	user.Packages = []Package{} // a new account has none; [] reads better than null
	return &user, nil
}

func (s *Server) getUser(ctx context.Context, username string) (*User, error) {
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

	// Capped: a prolific publisher should not turn one profile lookup into an
	// unbounded response.
	user.Packages, err = s.listPackages(ctx, PackageFilter{
		Username: user.Username,
		Limit:    packagesMaxLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list user packages: %w", err)
	}

	return &user, nil
}

func (s *Server) verifyLogin(ctx context.Context, login UserLogin) (*User, error) {
	user, err := s.getUser(ctx, login.Username)
	if errors.Is(err, ErrUserNotFound) {
		_, _ = argon2id.CreateHash(login.Password, argon2id.DefaultParams)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	match, err := argon2id.ComparePasswordAndHash(login.Password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("compare password: %w", err)
	}
	if !match {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

func (s *Server) changePassword(ctx context.Context, reset UserResetPassword) error {
	if _, err := s.verifyLogin(ctx, UserLogin{
		Username: reset.Username,
		Password: reset.CurrentPassword,
	}); err != nil {
		return err
	}

	hash, err := argon2id.CreateHash(reset.NewPassword, argon2id.DefaultParams)
	if err != nil {
		return fmt.Errorf("create hash: %w", err)
	}

	tag, err := s.db.Exec(ctx,
		`UPDATE users SET password_hash = $1 WHERE username = $2`, hash, reset.Username)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}

	return nil
}

func (s *Server) requireAuth(ctx context.Context, username, password string) error {
	var loginUser = UserLogin{
		Username: username,
		Password: password,
	}

	user, err := s.verifyLogin(ctx, loginUser)
	if err != nil || user == nil {
		return ErrInvalidCredentials
	}

	return nil
}

const packageColumns = `id, publisher_id, platform, name, description, created_at`

const (
	packagesDefaultLimit = 50
	packagesMaxLimit     = 200
)

// escapeLike neutralises LIKE wildcards in user input, so a search for "a_b"
// means a_b and not "a<anything>b".
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}

func (s *Server) listPackages(ctx context.Context, f PackageFilter) ([]Package, error) {
	var conds []string
	var args []any

	// add appends one arg and its condition, formatting the placeholder to the
	// arg's position. Values only ever reach the query as parameters.
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

	// CollectRows already returns an empty slice for no rows; it never returns
	// ErrNoRows. Dropping this error hides every failure as "no packages".
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

// attachReleases fills Releases on every package in one extra query, so a page
// of N packages costs 2 queries and not N+1. Newest release first.
func (s *Server) attachReleases(ctx context.Context, packages []Package) error {
	if len(packages) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, len(packages))
	byPackage := make(map[uuid.UUID]*Package, len(packages))
	for i := range packages {
		ids[i] = packages[i].ID
		// A package with no releases serialises as [] and not null.
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

// getPackageRow reads the packages row only. Callers that need Releases use
// getPackage; getRelease does not, and should not pay for the extra query.
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

	// attachReleases writes through the slice, so read the package back out of it.
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

	createdPackage, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Package])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackageExists
	}
	if err != nil {
		return nil, fmt.Errorf("collect package: %w", err)
	}

	createdPackage.Releases = []Release{} // a new package has none; [] reads better than null
	return &createdPackage, nil
}

func (s *Server) publishRelease(ctx context.Context, publisher User, packaged Package, release *Release) (*Release, error) {
	if release == nil {
		return nil, errors.New("publish release: no release given")
	}
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
