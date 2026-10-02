package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// newTestDB opens a fresh, migrated database in a per-test temp directory.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

// tableNames returns every user table in the database, excluding migration
// bookkeeping.
func tableNames(t *testing.T, sqlDB *sql.DB) map[string]struct{} {
	t.Helper()
	rows, err := sqlDB.QueryContext(t.Context(), `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'
		ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	names := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	return names
}

// tableColumns returns the column shape of a table as reported by PRAGMA
// table_info: "name:type:notnull:default:pk".
func tableColumns(t *testing.T, sqlDB *sql.DB, table string) map[string]string {
	t.Helper()
	rows, err := sqlDB.QueryContext(t.Context(), "PRAGMA table_info("+quoteIdent(table)+")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()

	cols := make(map[string]string)
	for rows.Next() {
		var (
			cid       int64
			name, typ string
			notNull   int64
			dflt      sql.NullString
			pk        int64
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		cols[name] = typ + ":" + boolStr(notNull) + ":" + dflt.String + ":" + boolStr(pk)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	return cols
}

func quoteIdent(s string) string { return `"` + s + `"` }

func boolStr(v int64) string {
	if v != 0 {
		return "true"
	}
	return "false"
}

// wantedTables is the full schema the plan requires. Any table added here
// must also exist in schema.sql AND migrations/000001_init.sql.
var wantedTables = []string{
	"audit_log",
	"blob_metadata",
	"blob_refs",
	"manifests",
	"repositories",
	"sessions",
	"tags",
	"uploads",
	"users",
}
