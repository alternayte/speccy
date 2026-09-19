-- +goose Up
-- DEC-019: one fine-grained personal access token per workspace, set by an admin.
CREATE TABLE github_connection (
    workspace_id    UUIDTEXT PRIMARY KEY REFERENCES workspace (id),
    token_encrypted BLOB NOT NULL,
    token_last4     TEXT NOT NULL,
    api_url         TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    updated_at      DATETIME NOT NULL
);

-- REQ-123: a repo, a branch, and a folder whose bundles Speccy reads.
CREATE TABLE github_source (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    repo         TEXT NOT NULL,
    branch       TEXT NOT NULL,
    path         TEXT NOT NULL,
    head_commit  TEXT NOT NULL DEFAULT '',
    synced_at    DATETIME,
    error        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    UNIQUE (workspace_id, repo, branch, path)
);

-- +goose Down
DROP TABLE github_source;
DROP TABLE github_connection;
