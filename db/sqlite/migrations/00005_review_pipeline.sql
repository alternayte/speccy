-- +goose Up
CREATE TABLE job (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    kind         TEXT NOT NULL,
    payload      JSONTEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    locked_until DATETIME,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL
);
CREATE INDEX job_queue ON job (status, created_at);

CREATE TABLE cache_entry (
    key_hash   TEXT PRIMARY KEY,
    result     JSONTEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE mcp_connection (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    name             TEXT NOT NULL,
    transport        TEXT NOT NULL CHECK (transport IN ('stdio', 'http')),
    command_or_url   JSONTEXT NOT NULL,
    secret_encrypted BLOB,
    secret_last4     TEXT NOT NULL,
    tool_allowlist   JSONTEXT NOT NULL,
    is_search        BOOLEAN NOT NULL,
    search_tool      TEXT NOT NULL,
    created_at       DATETIME NOT NULL,
    UNIQUE (workspace_id, name)
);

CREATE TABLE claim (
    id         UUIDTEXT PRIMARY KEY,
    run_id     UUIDTEXT NOT NULL REFERENCES review_run (id),
    text       TEXT NOT NULL,
    label      TEXT NOT NULL CHECK (label IN ('verified', 'contradicted', 'unverified')),
    reason     TEXT NOT NULL,
    sources    JSONTEXT NOT NULL,
    anchor     JSONTEXT NOT NULL
);
CREATE INDEX claim_run ON claim (run_id);

-- REQ-034, REQ-046: notes for the run report, such as "no search source configured".
ALTER TABLE review_run ADD COLUMN notes JSONTEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE review_run DROP COLUMN notes;
DROP TABLE claim;
DROP TABLE mcp_connection;
DROP TABLE cache_entry;
DROP TABLE job;
