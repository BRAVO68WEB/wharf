package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// schemaMigrationsTable tracks which migration versions have been applied.
// Created by the runner itself, not by any migration file.
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL DEFAULT (unixepoch())
)`

// migration is one versioned DDL file from the embedded migrations directory.
type migration struct {
	Version int64
	Name    string
	SQL     string
}

// loadMigrations parses the embedded migration files. File names must look
// like "000001_init.sql": a zero-padded version, an underscore, a name, and
// the .sql suffix. Returns them sorted ascending by version.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("db: read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	seen := make(map[int64]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("db: duplicate migration version %d in %q and %q", version, prev, entry.Name())
		}
		seen[version] = entry.Name()

		raw, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("db: read migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{
			Version: version,
			Name:    name,
			SQL:     string(raw),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("db: no migrations found in embedded migrations directory")
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

func parseMigrationName(filename string) (int64, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	dot := strings.IndexByte(base, '_')
	if dot <= 0 {
		return 0, "", fmt.Errorf("db: migration %q must be named <version>_<name>.sql", filename)
	}
	version, err := strconv.ParseInt(base[:dot], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("db: migration %q has non-numeric version: %w", filename, err)
	}
	return version, base[dot+1:], nil
}

// Migrate applies every embedded migration that the database has not yet
// seen, each inside its own transaction, and records it in schema_migrations.
//
// It is idempotent: calling it on an up-to-date database is a no-op and
// returns nil. Older databases are upgraded forward only (no downgrades).
func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	if sqlDB == nil {
		return fmt.Errorf("db: migrate: nil database")
	}
	if _, err := sqlDB.ExecContext(ctx, schemaMigrationsTable); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, sqlDB)
	if err != nil {
		return err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if err := applyMigration(ctx, sqlDB, m); err != nil {
			return err
		}
	}
	return nil
}

func appliedVersions(ctx context.Context, sqlDB *sql.DB) (map[int64]struct{}, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]struct{})
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, sqlDB *sql.DB, m migration) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin migration %06d: %w", m.Version, err)
	}
	// ErrTxDone after commit is expected; only the failure paths surface errors.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("db: apply migration %06d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES (?)`, m.Version,
	); err != nil {
		return fmt.Errorf("db: record migration %06d_%s: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit migration %06d_%s: %w", m.Version, m.Name, err)
	}
	return nil
}
