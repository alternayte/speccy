-- +goose Up
-- SDD §7.2: the job worker. Postgres claims with SKIP LOCKED.
CREATE TABLE job (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    kind         text NOT NULL,
    payload      jsonb NOT NULL,
    status       text NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts     bigint NOT NULL DEFAULT 0,
    locked_until timestamptz,
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL
);
CREATE INDEX job_queue ON job (status, created_at);

-- REQ-021, §8.10: cached step results.
CREATE TABLE cache_entry (
    key_hash   text PRIMARY KEY,
    result     jsonb NOT NULL,
    created_at timestamptz NOT NULL
);

-- REQ-112: MCP client connections. Secrets are sealed (§14.1).
CREATE TABLE mcp_connection (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspace (id),
    name             text NOT NULL,
    transport        text NOT NULL CHECK (transport IN ('stdio', 'http')),
    command_or_url   jsonb NOT NULL,
    secret_encrypted bytea,
    secret_last4     text NOT NULL,
    tool_allowlist   jsonb NOT NULL,
    is_search        boolean NOT NULL,
    search_tool      text NOT NULL,
    created_at       timestamptz NOT NULL,
    UNIQUE (workspace_id, name)
);

-- REQ-030, REQ-031: the claims of a run and their labels. Unverified and contradicted claims
-- are also findings (REQ-032).
CREATE TABLE claim (
    id         uuid PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES review_run (id),
    text       text NOT NULL,
    label      text NOT NULL CHECK (label IN ('verified', 'contradicted', 'unverified')),
    reason     text NOT NULL,
    sources    jsonb NOT NULL,
    anchor     jsonb NOT NULL
);
CREATE INDEX claim_run ON claim (run_id);

-- +goose Down
DROP TABLE claim;
DROP TABLE mcp_connection;
DROP TABLE cache_entry;
DROP TABLE job;
