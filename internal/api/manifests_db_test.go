package api_test

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

// newTestSQLDB opens a fresh, migrated SQLite database in a per-test temp
// directory: the routes run against the real schema, not a mock.
func newTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

// newTestQueries returns the generated query surface over a fresh database.
func newTestQueries(t *testing.T) db.Querier {
	t.Helper()
	return db.New(newTestSQLDB(t))
}

// newManifestRouter wires the registry route tree over a fake blob store and
// a real, empty SQLite database.
func newManifestRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	q := newTestQueries(t)
	return api.NewRouter(newFakeStorage(), q), q
}
