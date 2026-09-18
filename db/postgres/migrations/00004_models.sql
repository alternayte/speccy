-- +goose Up
-- REQ-100 to REQ-104, SDD §14.1: backends, role assignments, and the monthly token budget.
CREATE TABLE model_backend (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspace (id),
    kind             text NOT NULL CHECK (kind IN ('openai', 'anthropic', 'openrouter', 'deepseek', 'agent_cli', 'fake')),
    name             text NOT NULL,
    config           jsonb NOT NULL,
    secret_encrypted bytea,
    secret_last4     text NOT NULL,
    created_at       timestamptz NOT NULL,
    UNIQUE (workspace_id, name)
);

CREATE TABLE role_assignment (
    workspace_id     uuid NOT NULL REFERENCES workspace (id),
    role             text NOT NULL CHECK (role IN ('reviewer', 'reader_1', 'reader_2', 'reader_3', 'judge', 'writer')),
    backend_id       uuid NOT NULL REFERENCES model_backend (id),
    model            text NOT NULL,
    price_in_per_mtok  double precision NOT NULL,
    price_out_per_mtok double precision NOT NULL,
    PRIMARY KEY (workspace_id, role)
);

CREATE TABLE budget (
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    month        text NOT NULL,
    token_limit  bigint,
    tokens_used  bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, month)
);

-- +goose Down
DROP TABLE budget;
DROP TABLE role_assignment;
DROP TABLE model_backend;
