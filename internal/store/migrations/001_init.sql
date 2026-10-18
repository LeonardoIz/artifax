CREATE TABLE docs (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    visibility      TEXT NOT NULL CHECK (visibility IN ('public', 'unlisted', 'private')),
    blob_sha256     TEXT NOT NULL,
    byte_size       INTEGER NOT NULL,
    source_filename TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
) STRICT;

CREATE INDEX docs_updated_idx ON docs (updated_at DESC, id DESC);
CREATE INDEX docs_blob_idx ON docs (blob_sha256);

CREATE TABLE tokens (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    secret_hash  TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
) STRICT;

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    subject    TEXT NOT NULL,
    email      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_expires_idx ON sessions (expires_at);
