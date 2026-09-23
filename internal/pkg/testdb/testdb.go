// Package testdb gives a test a real, migrated PostgreSQL database.
//
// WHY THIS EXISTS
//
// Repository coverage was measured at 0.0% across 264 files on 2026-09-23.
// Every SQL string in the system was untested - not because anyone thought
// that was fine, but because CLAUDE.md recorded that building test
// infrastructure was "a separate project" and the right call was to spend the
// time on billing coverage instead. That was true when it was written.
//
// It turned out most of the infrastructure already existed. TEST_DB_NAME is in
// .env, `make migrate-test` runs, and cmd/migrate/main.go already switches on
// TEST_DB=true. What was missing was this file.
//
// WHY NOT TESTCONTAINERS
//
// Postgres is already running locally as the `bedge-postgres` container. Adding
// a library that starts another one would be slower, add a dependency, and need
// Docker socket access from the test process. CLAUDE.md warns against spending
// a sprint here and is right.
//
// HOW IT IS FAST
//
// Migrating 50 migrations per test package would dominate the suite. Instead
// the migrations run ONCE into a template database, and each package clones it:
//
//	CREATE DATABASE bedge_test_<pkg> TEMPLATE bedge_test_tmpl
//
// Postgres implements that as a file copy of the template's directory, so a
// clone costs roughly 100ms regardless of how many migrations produced it. The
// cost is per package, not per test.
//
// WHAT IT IS FOR
//
// Things a mock cannot test, and which the service-layer suite therefore has
// never checked:
//
//   - The GIST exclusion constraint. The chaos suite exercises it through HTTP,
//     which cannot distinguish "the constraint held" from "the requests
//     happened not to overlap".
//   - The CASE WHEN presence/value pairs that let a nullable column be cleared.
//     The COALESCE they replaced silently ignored {"field": null}, returned
//     200, and wrote nothing.
//   - CHECK constraints, defaults, cascade behaviour, and every ON CONFLICT.
//
// USAGE
//
// Tests using this must carry the dbtest build tag so `go test ./...` stays
// fast and only CI pays:
//
//	//go:build dbtest
//
//	func TestSomething(t *testing.T) {
//	    pool := testdb.New(t)
//	    repo := booking.NewRepository(pool)
//	    ...
//	}
//
// Run them with: make test-db
package testdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // migrate driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // file:// source
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// templateDB is migrated once and cloned per package.
const templateDB = "bedge_test_tmpl"

// templateLockID namespaces the Postgres advisory lock that serialises
// template creation. Test binaries for different packages run in parallel by
// default, so without this several of them race to create and migrate the same
// template and all but one fail with "database already exists".
const templateLockID = 8412337

var (
	once    sync.Once
	onceErr error
)

// New returns a pool against a fresh database cloned from the migrated
// template. The database is dropped when the test finishes.
//
// It calls t.Fatal rather than returning an error: a test that cannot get a
// database has nothing to assert, and every caller would write the same three
// lines.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()

	once.Do(func() { onceErr = ensureTemplate() })
	if onceErr != nil {
		t.Fatalf("testdb: preparing the template database: %v", onceErr)
	}

	name := cloneName(t)
	admin := mustConnect(t, maintenanceDSN())

	// A template cannot be cloned while anything is connected to it, and a
	// clone cannot be created if the name is already taken by a previous
	// crashed run. Drop first, unconditionally.
	exec(t, admin, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name))
	exec(t, admin, fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, name, templateDB))
	admin.Close()

	pool, err := pgxpool.New(context.Background(), dsnFor(name))
	if err != nil {
		t.Fatalf("testdb: connecting to %s: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		// A new admin connection: the pool above is closed, and the one used
		// to create the database was closed immediately after.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a, err := pgxpool.New(ctx, maintenanceDSN())
		if err != nil {
			t.Logf("testdb: cleanup could not connect, leaving %s behind: %v", name, err)
			return
		}
		defer a.Close()
		if _, err := a.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
			t.Logf("testdb: could not drop %s: %v", name, err)
		}
	})

	return pool
}

// ensureTemplate creates and migrates the template database if it is not
// already present. Serialised across parallel test binaries by an advisory
// lock held on the maintenance database.
func ensureTemplate() error {
	loadEnv()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgxpool.New(ctx, maintenanceDSN())
	if err != nil {
		return fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer admin.Close()

	// Hold the lock for the whole check-and-create. Taking it after the
	// existence check would reintroduce the race it exists to close.
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock($1)`, templateLockID); err != nil {
		return fmt.Errorf("take advisory lock: %w", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, templateLockID)
	}()

	var exists bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`,
		templateDB).Scan(&exists); err != nil {
		return fmt.Errorf("check for template: %w", err)
	}

	if exists {
		// Already migrated by an earlier run. Migrations are append-only here,
		// so a template built from an older set would be missing tables that
		// newer code needs - refresh it whenever the migration count moved.
		current, err := templateVersion(ctx)
		if err == nil && current == latestMigrationVersion() {
			return nil
		}
		if _, err := admin.Exec(ctx,
			fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, templateDB)); err != nil {
			return fmt.Errorf("drop stale template: %w", err)
		}
	}

	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, templateDB)); err != nil {
		return fmt.Errorf("create template: %w", err)
	}

	dir, err := migrationsDir()
	if err != nil {
		return err
	}

	m, err := migrate.New("file://"+dir, migrateDSN(templateDB))
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate template: %w", err)
	}

	return nil
}

// templateVersion reports the migration version recorded in the template.
func templateVersion(ctx context.Context) (uint, error) {
	p, err := pgxpool.New(ctx, dsnFor(templateDB))
	if err != nil {
		return 0, err
	}
	defer p.Close()

	var v uint
	var dirty bool
	if err := p.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&v, &dirty); err != nil {
		return 0, err
	}
	if dirty {
		return 0, fmt.Errorf("template schema_migrations is dirty at %d", v)
	}
	return v, nil
}

// latestMigrationVersion is the highest numbered .up.sql on disk.
func latestMigrationVersion() uint {
	dir, err := migrationsDir()
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var highest uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		var n uint
		if _, err := fmt.Sscanf(name, "%d_", &n); err == nil && n > highest {
			highest = n
		}
	}
	return highest
}

// cloneName derives a database name from the test's package and name.
//
// Postgres identifiers are capped at 63 bytes and the name must be a valid
// identifier, so anything unusual is replaced and the result truncated.
func cloneName(t *testing.T) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '_'
		}
	}, t.Name())

	name := "bedge_test_" + safe
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// ── environment ────────────────────────────────────────────────────────────

// loadEnv reads the repo's .env if the DB variables are not already set, so
// tests work from a bare `go test` as well as from make.
func loadEnv() {
	if os.Getenv("DB_HOST") != "" {
		return
	}
	if root, err := repoRoot(); err == nil {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// maintenanceDSN points at a database that always exists, so CREATE/DROP
// DATABASE can be issued from a connection that is not inside the target.
func maintenanceDSN() string { return dsnFor("postgres") }

func dsnFor(db string) string {
	loadEnv()
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		env("DB_USER", "postgres"),
		env("DB_PASSWORD", "postgres"),
		env("DB_HOST", "localhost"),
		env("DB_PORT", "5432"),
		db,
	)
}

// migrateDSN is the same target in the scheme golang-migrate expects.
func migrateDSN(db string) string { return dsnFor(db) }

// migrationsDir resolves db/migrations from the repo root, because a test's
// working directory is its own package directory rather than the root.
func migrationsDir() (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "db", "migrations")
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("migrations directory not found at %s: %w", dir, err)
	}
	return dir, nil
}

// repoRoot walks up from the working directory looking for go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// ── small helpers ──────────────────────────────────────────────────────────

func mustConnect(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	return p
}

func exec(t *testing.T, p *pgxpool.Pool, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := p.Exec(ctx, sql); err != nil {
		t.Fatalf("testdb: %s: %v", sql, err)
	}
}
