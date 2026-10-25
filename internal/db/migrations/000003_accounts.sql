-- 000003_accounts: service accounts and revocable docker-login tokens.
-- A service account has no usable password. docker login sends the token
-- as the password. Human accounts keep their password and may also hold tokens.

ALTER TABLE users ADD COLUMN is_service INTEGER NOT NULL DEFAULT 0;

CREATE TABLE access_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL DEFAULT '',
    token_hash TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    revoked_at INTEGER
);

CREATE INDEX idx_access_tokens_user ON access_tokens (user_id);
