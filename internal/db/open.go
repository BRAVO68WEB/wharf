// Package db owns the SQLite metadata store: schema migrations and
// type-safe generated queries.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	// modernc.org/sqlite is the pure-Go SQLite driver (no CGO). It registers
	// itself under the driver name "sqlite".
	_ "modernc.org/sqlite"
)

const (
	// DriverName is the database/sql driver registered by modernc.org/sqlite.
	DriverName = "sqlite"

	// BusyTimeoutMS is how long a connection waits on a locked database
	// before failing with SQLITE_BUSY. The registry serves concurrent
	// dashboard reads alongside push writes, so lock waits are expected.
	BusyTimeoutMS = 5000

	// maxOpenConns caps the connection pool. SQLite allows one writer at a
	// time; WAL mode keeps readers unblocked. A small pool plus the busy
	// timeout keeps write contention predictable without serializing reads.
	maxOpenConns = 8
)

// Open opens (creating if needed) the SQLite database at path and configures
// it for concurrent registry use:
//
//   - journal_mode=WAL      readers never block the writer (and vice versa)
//   - busy_timeout=5000ms   lock contention retries instead of failing
//   - foreign_keys=ON       referential integrity enforced per connection
//
// Pragmas are set through the DSN so every pooled connection gets them.
// Callers must still run Migrate before use.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("db: open: empty database path")
	}

	sqlDB, err := sql.Open(DriverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", path, err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)

	// Force the DSN/pragmas to be applied now so configuration problems
	// surface at startup rather than on first query.
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: ping %q: %w", path, err)
	}
	return sqlDB, nil
}

// dsn builds the modernc.org/sqlite DSN with per-connection pragmas.
func dsn(path string) string {
	query := url.Values{}
	// Order matters only for readability; each _pragma is applied per conn.
	query.Set("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(ON)")

	u := url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}
	return u.String()
}

// OpenAndMigrate is the convenience entry point used at startup: open the
// database, apply pending migrations, and return the ready pool.
func OpenAndMigrate(ctx context.Context, path string) (*sql.DB, error) {
	sqlDB, err := Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}
