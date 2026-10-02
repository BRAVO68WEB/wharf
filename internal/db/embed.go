package db

import "embed"

// migrationFS holds the versioned schema migrations applied by Migrate.
// Files are named "<zero-padded version>_<name>.sql", e.g. 000001_init.sql.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// schemaFS holds the canonical full schema (internal/db/schema.sql), used by
// tests to prove the migration chain and the canonical schema agree.
//
//go:embed schema.sql
var schemaFS embed.FS
