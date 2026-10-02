package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpen_WALAndBusyTimeout: Given a freshly opened database, When the
// connection pragmas are queried, Then the store runs in WAL mode with a
// 5000ms busy timeout and foreign keys enforced.
func TestOpen_WALAndBusyTimeout(t *testing.T) {
	sqlDB := newTestDB(t)

	var journal string
	if err := sqlDB.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var busyTimeout int64
	if err := sqlDB.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != BusyTimeoutMS {
		t.Errorf("busy_timeout = %d, want %d", busyTimeout, BusyTimeoutMS)
	}

	var foreignKeys int64
	if err := sqlDB.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
}

// TestOpen_EmptyPath: Given an empty path, When Open runs, Then it fails with
// a clear error.
func TestOpen_EmptyPath(t *testing.T) {
	if _, err := Open(t.Context(), ""); err == nil {
		t.Fatal("Open(\"\") = nil error, want error")
	}
}

// TestOpenAndMigrate_CorruptFile: Given a file that is not a SQLite database,
// When OpenAndMigrate runs, Then it fails with an error that names the store
// (db:) instead of panicking or silently creating tables.
func TestOpenAndMigrate_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	garbage := []byte("this is definitely not a sqlite database file")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, err := OpenAndMigrate(t.Context(), path)
	if err == nil {
		t.Fatal("OpenAndMigrate(corrupt) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "db:") {
		t.Errorf("error %q lacks the db: prefix for clear ownership", err)
	}
}
