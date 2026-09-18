-- +goose Up
CREATE TABLE model_backend (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    kind             TEXT NOT NULL CHECK (kind IN ('openai', 'anthropic', 'openrouter', 'deepseek', 'agent_cli', 'fake')),
    name             TEXT NOT NULL,
    config           JSONTEXT NOT NULL,
    secret_encrypted BLOB,
    secret_last4     TEXT NOT NULL,
    created_at       DATETIME NOT NULL,
    UNIQUE (workspace_id, name)
);

CREATE TABLE role_assignment (
    workspace_id       UUIDTEXT NOT NULL REFERENCES workspace (id),
    role               TEXT NOT NULL CHECK (role IN ('reviewer', 'reader_1', 'reader_2', 'reader_3', 'judge', 'writer')),
    backend_id         UUIDTEXT NOT NULL REFERENCES model_backend (id),
    model              TEXT NOT NULL,
    price_in_per_mtok  REAL NOT NULL,
    price_out_per_mtok REAL NOT NULL,
    PRIMARY KEY (workspace_id, role)
);

CREATE TABLE budget (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    month        TEXT NOT NULL,
    token_limit  INTEGER,
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, month)
);

-- +goose Down
DROP TABLE budget;
DROP TABLE role_assignment;
DROP TABLE model_backend;
