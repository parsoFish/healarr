package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations"

// migrate applies every migration in migrationsDir whose version number is
// greater than the schema's current version. Each migration runs in its
// own transaction, then bumps schema_meta.version, so a failed migration
// never leaves the schema partially applied. Rerunning migrate on an
// already-current database is a no-op.
func migrate(ctx context.Context, db *sql.DB) error {
	names, err := sortedMigrationNames()
	if err != nil {
		return err
	}

	current, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}
		if err := applyMigration(ctx, db, name, version); err != nil {
			return err
		}
	}
	return nil
}

// sortedMigrationNames lists the embedded migration filenames in ascending
// order. fs.ReadDir already sorts by name, but we sort explicitly so the
// ordering guarantee doesn't depend on that implementation detail.
func sortedMigrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("store: list migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// migrationVersion extracts the leading numeric prefix of a migration
// filename, e.g. "0001_init.sql" -> 1.
func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("store: migration %q missing version prefix", name)
	}
	v, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("store: migration %q has non-numeric version prefix: %w", name, err)
	}
	return v, nil
}

// currentSchemaVersion reads schema_meta.version, treating a database
// without a schema_meta table yet (a brand new file) as version 0.
func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_meta'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("store: check schema_meta: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}

	var v int
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_meta`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read schema_meta: %w", err)
	}
	return v, nil
}

// applyMigration runs one migration file's SQL and records its version,
// both inside a single transaction.
func applyMigration(ctx context.Context, db *sql.DB, name string, version int) (err error) {
	script, err := migrationsFS.ReadFile(migrationsDir + "/" + name)
	if err != nil {
		return fmt.Errorf("store: read migration %s: %w", name, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %s: %w", name, err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("store: rollback migration %s: %w", name, rbErr))
		}
	}()

	if _, err = tx.ExecContext(ctx, string(script)); err != nil {
		return fmt.Errorf("store: apply migration %s: %w", name, err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE schema_meta SET version = ?`, version); err != nil {
		return fmt.Errorf("store: record migration %s version: %w", name, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %s: %w", name, err)
	}
	committed = true

	return nil
}
