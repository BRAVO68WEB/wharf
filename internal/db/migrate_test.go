package db

import (
	"path/filepath"
	"testing"
)

// TestMigrate_FromEmpty_CreatesAllTables: Given an empty database file,
// When Migrate runs, Then every planned table exists and each migration
// version is recorded exactly once.
func TestMigrate_FromEmpty_CreatesAllTables(t *testing.T) {
	sqlDB := newTestDB(t)

	got := tableNames(t, sqlDB)
	for _, want := range wantedTables {
		if _, ok := got[want]; !ok {
			t.Errorf("table %q missing after migration", want)
		}
	}
	if len(got) != len(wantedTables) {
		t.Errorf("unexpected table set: got %d tables, want %d: %v", len(got), len(wantedTables), got)
	}

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	rows, err := sqlDB.QueryContext(t.Context(), `SELECT count(*) FROM schema_migrations`)
	if err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	defer rows.Close()
	var applied int64
	if !rows.Next() {
		t.Fatal("count schema_migrations returned no row")
	}
	if err := rows.Scan(&applied); err != nil {
		t.Fatalf("scan applied count: %v", err)
	}
	if applied != int64(len(migrations)) {
		t.Errorf("applied migrations = %d, want %d", applied, len(migrations))
	}
}

// TestMigrate_Idempotent: Given a migrated database, When Migrate runs again,
// Then it succeeds, re-applies nothing, and leaves the schema unchanged.
func TestMigrate_Idempotent(t *testing.T) {
	sqlDB := newTestDB(t)
	before := tableNames(t, sqlDB)

	if err := Migrate(t.Context(), sqlDB); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if err := Migrate(t.Context(), sqlDB); err != nil {
		t.Fatalf("third Migrate: %v", err)
	}

	after := tableNames(t, sqlDB)
	if len(before) != len(after) {
		t.Fatalf("table set changed across re-migration: before=%v after=%v", before, after)
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Errorf("table %q disappeared after re-migration", name)
		}
	}

	var applied int64
	if err := sqlDB.QueryRowContext(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if applied != int64(len(migrations)) {
		t.Errorf("applied migrations = %d, want %d (re-run must not double-record)", applied, len(migrations))
	}
}

// TestMigrate_VersionOrder: Given embedded migrations, When they are loaded,
// Then versions are strictly increasing and every name parses.
func TestMigrate_VersionOrder(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no embedded migrations")
	}
	var prev int64
	for i, m := range migrations {
		if m.Version <= prev && i > 0 {
			t.Errorf("migration %d (%s) out of order: version %d after %d", i, m.Name, m.Version, prev)
		}
		if m.SQL == "" {
			t.Errorf("migration %06d_%s has empty SQL", m.Version, m.Name)
		}
		prev = m.Version
	}
}

// TestMigrate_NilDatabase: Given a nil *sql.DB, When Migrate runs, Then it
// returns a clear error instead of panicking.
func TestMigrate_NilDatabase(t *testing.T) {
	if err := Migrate(t.Context(), nil); err == nil {
		t.Fatal("Migrate(nil) = nil, want error")
	}
}

// TestSchemaParity: Given the canonical schema.sql and the migration chain,
// When each is applied to its own empty database, Then both produce identical
// tables and identical column shapes. This is the drift guard between the two
// schema sources.
func TestSchemaParity(t *testing.T) {
	migrated := newTestDB(t)

	fromSchema, err := Open(t.Context(), filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatalf("open schema db: %v", err)
	}
	t.Cleanup(func() { _ = fromSchema.Close() })

	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read embedded schema.sql: %v", err)
	}
	if _, err := fromSchema.ExecContext(t.Context(), string(raw)); err != nil {
		t.Fatalf("apply schema.sql: %v", err)
	}

	wantTables := tableNames(t, fromSchema)
	gotTables := tableNames(t, migrated)
	for name := range wantTables {
		if _, ok := gotTables[name]; !ok {
			t.Errorf("table %q in schema.sql but missing from migrations", name)
		}
	}
	for name := range gotTables {
		if _, ok := wantTables[name]; !ok {
			t.Errorf("table %q in migrations but missing from schema.sql", name)
		}
	}

	for name := range wantTables {
		wantCols := tableColumns(t, fromSchema, name)
		gotCols := tableColumns(t, migrated, name)
		if len(wantCols) != len(gotCols) {
			t.Errorf("table %s: column count differs (schema.sql=%v migrations=%v)", name, wantCols, gotCols)
			continue
		}
		for col, shape := range wantCols {
			if gotShape, ok := gotCols[col]; !ok {
				t.Errorf("table %s: column %q missing from migrations", name, col)
			} else if gotShape != shape {
				t.Errorf("table %s column %s: shape mismatch schema.sql=%q migrations=%q", name, col, shape, gotShape)
			}
		}
	}
}
