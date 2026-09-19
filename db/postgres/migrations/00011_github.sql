-- +goose Up
-- DEC-019: one fine-grained personal access token per workspace, set by an admin.
CREATE TABLE github_connection (
    workspace_id    uuid PRIMARY KEY REFERENCES workspace (id),
    token_encrypted bytea NOT NULL,
    token_last4     text NOT NULL,
    api_url         text NOT NULL,
    updated_by      text NOT NULL,
    updated_at      timestamptz NOT NULL
);

-- REQ-123: a repo, a branch, and a folder whose bundles Speccy reads.
CREATE TABLE github_source (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    repo         text NOT NULL,
    branch       text NOT NULL,
    path         text NOT NULL,
    head_commit  text NOT NULL DEFAULT '',
    synced_at    timestamptz,
    error        text NOT NULL DEFAULT '',
    created_by   text NOT NULL,
    created_at   timestamptz NOT NULL,
    UNIQUE (workspace_id, repo, branch, path)
);

-- +goose Down
DROP TABLE github_source;
DROP TABLE github_connection;
