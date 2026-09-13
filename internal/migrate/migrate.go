// Package migrate applies SQL migrations bundled into the binary.
//
// Why a 60 line migrator instead of golang-migrate/goose: the schema is three tables, it
// must run automatically at startup (the app has to be "clone and run"), and being
// embedded means the binary is self-contained - no external files to ship, no version
// skew between the binary and the SQL files next to it. Each migration runs inside a
// transaction and is recorded in schema_migrations, so the process is idempotent and
// crash safe.
package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var files embed.FS

// Migration is one embedded SQL file.
type Migration struct {
	Version string // file name without extension, e.g. "0001_init"
	SQL     string
}

// List returns the embedded migrations ordered by file name.
func List() ([]Migration, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded migrations: %w", err)
	}

	var out []Migration

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}

		raw, err := files.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", e.Name(), err)
		}

		out = append(out, Migration{
			Version: strings.TrimSuffix(e.Name(), ".sql"),
			SQL:     string(raw),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })

	return out, nil
}

// Up applies all pending migrations. It is safe to call on every boot.
func Up(ctx context.Context, pool *pgxpool.Pool) (applied []string, err error) {
	if _, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("cannot ensure schema_migrations: %w", err)
	}

	migrations, err := List()
	if err != nil {
		return nil, err
	}

	done, err := appliedVersions(ctx, pool)
	if err != nil {
		return nil, err
	}

	for _, m := range migrations {
		if done[m.Version] {
			continue
		}

		if err = applyOne(ctx, pool, m); err != nil {
			return applied, fmt.Errorf("migration %s failed: %w", m.Version, err)
		}

		applied = append(applied, m.Version)
	}

	return applied, nil
}

func applyOne(ctx context.Context, pool *pgxpool.Pool, m Migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, m.SQL); err != nil {
		return err
	}

	if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, m.Version); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("cannot read applied migrations: %w", err)
	}

	defer rows.Close()

	out := make(map[string]bool)

	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}

		out[v] = true
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot read applied migrations: %w", err)
	}

	return out, nil
}

// ErrNotInitialized is returned by Check when the schema has never been applied.
var ErrNotInitialized = errors.New("database schema is not initialized")

// Check verifies that every embedded migration has been applied.
func Check(ctx context.Context, pool *pgxpool.Pool) error {
	var count int

	if err := pool.QueryRow(ctx, `SELECT 1 FROM information_schema.tables
		WHERE table_name = 'schema_migrations'`).Scan(&count); err != nil {
		return ErrNotInitialized
	}

	done, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	migrations, err := List()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if !done[m.Version] {
			return fmt.Errorf("%w: %s is missing", ErrNotInitialized, m.Version)
		}
	}

	return nil
}
