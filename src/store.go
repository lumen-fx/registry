package src

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alexedwards/argon2id"
	"github.com/jackc/pgx/v5"
)

const userColumns = `id, username, email, password_hash, created_at`

var ErrUserExists = errors.New("user already exists")
var ErrUserNotFound = errors.New("user not found")
var ErrInvalidCredentials = errors.New("invalid credentials")

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

	return packages, nil
}
