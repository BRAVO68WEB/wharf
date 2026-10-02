-- 000001_init: initial registry metadata schema.
-- Keep in sync with internal/db/schema.sql (TestSchemaParity enforces).
-- schema_migrations itself is created by the migration runner and is
-- intentionally NOT defined here.

CREATE TABLE IF NOT EXISTS repositories (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS manifests (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id    INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    digest     TEXT    NOT NULL,
    media_type TEXT    NOT NULL,
    payload    BLOB    NOT NULL,
    size       INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (repo_id, digest)
);

CREATE TABLE IF NOT EXISTS tags (
    repo_id         INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    manifest_digest TEXT    NOT NULL,
    updated_at      INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (repo_id, name),
    FOREIGN KEY (repo_id, manifest_digest) REFERENCES manifests (repo_id, digest) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS blob_refs (
    digest  TEXT    NOT NULL,
    repo_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    PRIMARY KEY (digest, repo_id)
);

CREATE TABLE IF NOT EXISTS blob_metadata (
    digest     TEXT    PRIMARY KEY,
    size       INTEGER NOT NULL,
    media_type TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS uploads (
    uuid       TEXT    PRIMARY KEY,
    repo_id    INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    digest     TEXT    NOT NULL DEFAULT '',
    "offset"   INTEGER NOT NULL DEFAULT 0,
    size       INTEGER NOT NULL DEFAULT 0,
    temp_path  TEXT    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    action     TEXT    NOT NULL,
    user_id    INTEGER REFERENCES users (id) ON DELETE SET NULL,
    target     TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_manifests_repo_id ON manifests (repo_id);
CREATE INDEX IF NOT EXISTS idx_tags_manifest ON tags (repo_id, manifest_digest);
CREATE INDEX IF NOT EXISTS idx_blob_refs_repo ON blob_refs (repo_id);
CREATE INDEX IF NOT EXISTS idx_uploads_expires ON uploads (expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_log (target, id);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log (created_at);
