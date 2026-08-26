package src

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/lumen-fx/registry/server/migrations"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateURLPicksThePgxDriver(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"postgres://u:p@host:5432/db", "pgx5://u:p@host:5432/db"},
		{"postgresql://u:p@host:5432/db", "pgx5://u:p@host:5432/db"},
		{"pgx5://u:p@host:5432/db", "pgx5://u:p@host:5432/db"},
	} {
		if got := migrateURL(c.in); got != c.want {
			t.Errorf("migrateURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// brokenFS fails every read, which is what an unreadable migration directory
// looks like from inside iofs.
type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

func TestRunMigrationsReportsAnUnreadableFilesystem(t *testing.T) {
	err := runMigrations(brokenFS{}, "postgres://u:p@127.0.0.1:1/db")
	if err == nil {
		t.Fatal("runMigrations accepted an unreadable filesystem")
	}
	if !strings.Contains(err.Error(), "read migrations") {
		t.Errorf("error = %v, want it to name the read failure", err)
	}
}

func TestRunMigrationsNeedsADatabaseURL(t *testing.T) {
	if err := RunMigrations(""); err == nil {
		t.Error("RunMigrations(\"\") returned nil, want an error")
	}
	if err := RunMigrations("postgres://user:pass@127.0.0.1:1/nothing"); err == nil {
		t.Error("RunMigrations reached an unreachable database without error")
	}
}

func TestRunMigrationsIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	// TestMain already migrated. A second run has nothing to do and must not
	// report that as a failure.
	if err := RunMigrations(dsn); err != nil {
		t.Errorf("second RunMigrations = %v, want nil", err)
	}
}

// TestMigrationsRollBackAndForward exercises the down files, then leaves the
// schema as it found it so the other tests still have their tables.
func TestMigrationsRollBackAndForward(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL(dsn))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer m.Close()

	// Put the schema back whatever happens below.
	t.Cleanup(func() {
		if err := RunMigrations(dsn); err != nil {
			t.Fatalf("restore schema: %v", err)
		}
	})

	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tableExists(t, "packages") {
		t.Error("packages survived a full rollback")
	}

	if err := m.Up(); err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, table := range []string{"users", "packages", "releases"} {
		if !tableExists(t, table) {
			t.Errorf("%s is missing after migrating back up", table)
		}
	}
}

func tableExists(t *testing.T, name string) bool {
	t.Helper()

	var exists bool
	err := testPool.QueryRow(context.Background(),
		`SELECT EXISTS (
		     SELECT 1 FROM information_schema.tables
		     WHERE table_schema = 'public' AND table_name = $1
		 )`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check %s: %v", name, err)
	}

	return exists
}

// TestRunMigrationsReportsADirtyDatabase covers the failure path. A dirty
// version is how migrate records a migration that died halfway.
func TestRunMigrationsReportsADirtyDatabase(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	const name = "lpm_dirty_test"

	// Its own database, so the suite's schema is left alone.
	if _, err := testPool.Exec(ctx, `DROP DATABASE IF EXISTS `+name); err != nil {
		t.Fatalf("drop database: %v", err)
	}
	if _, err := testPool.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(ctx, `DROP DATABASE IF EXISTS `+name); err != nil {
			t.Errorf("drop database: %v", err)
		}
	})

	scratch := swapDatabase(dsn, name)
	if err := RunMigrations(scratch); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	pool, err := pgxpool.New(ctx, scratch)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatalf("dirty the version: %v", err)
	}
	pool.Close()

	if err := RunMigrations(scratch); err == nil {
		t.Error("RunMigrations returned nil against a dirty database")
	}
}

// swapDatabase points a connection string at a different database name.
func swapDatabase(dsn, name string) string {
	base, _, found := strings.Cut(dsn, "?")
	query := ""
	if found {
		query = dsn[len(base):]
	}
	return base[:strings.LastIndex(base, "/")] + "/" + name + query
}
