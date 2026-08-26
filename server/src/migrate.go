package src

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/lumen-fx/registry/server/migrations"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// RunMigrations brings the database up to the newest embedded migration. It is
// safe to call on a database that is already current.
func RunMigrations(dsn string) error {
	return runMigrations(migrations.FS, dsn)
}

// runMigrations takes the filesystem so a test can hand it a broken one.
func runMigrations(fsys fs.FS, dsn string) error {
	if dsn == "" {
		return errors.New("migrate: no database url")
	}

	source, err := iofs.New(fsys, ".")
	if err != nil {
		return fmt.Errorf("migrate: read migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL(dsn))
	if err != nil {
		return fmt.Errorf("migrate: open database: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: apply: %w", err)
	}

	return nil
}

// migrateURL picks the pgx driver. migrate registers drivers by scheme, and
// postgres:// would reach for lib/pq, which this module does not build.
func migrateURL(dsn string) string {
	for _, scheme := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dsn, scheme) {
			return "pgx5://" + strings.TrimPrefix(dsn, scheme)
		}
	}
	return dsn
}
