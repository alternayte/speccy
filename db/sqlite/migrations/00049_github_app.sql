-- +goose Up
-- The GitHub App of the workspace (docs/specs/github-app.md). A workspace holds an App or a
-- token in github_connection, never both. The secrets are sealed with SPECCY_MASTER_KEY.
CREATE TABLE github_app (
    workspace_id                UUIDTEXT PRIMARY KEY REFERENCES workspace (id),
    api_url                     TEXT NOT NULL,
    app_id                      INTEGER NOT NULL,
    slug                        TEXT NOT NULL,
    owner_login                 TEXT NOT NULL,
    owner_is_org                BOOLEAN NOT NULL,
    client_id                   TEXT NOT NULL,
    client_secret_encrypted     BLOB NOT NULL,
    private_key_encrypted       BLOB NOT NULL,
    webhook_secret_encrypted    BLOB NOT NULL,
    installation_id             INTEGER,
    created_by                  TEXT NOT NULL,
    created_at                  DATETIME NOT NULL,
    updated_at                  DATETIME NOT NULL
);

-- The GitHub user token of one person, from the App's user OAuth. Doc writes go out as that
-- person. The key is the GitHub account ID, which auth-all links to the Speccy account.
CREATE TABLE github_user_token (
    workspace_id        UUIDTEXT NOT NULL REFERENCES workspace (id),
    github_user_id      TEXT NOT NULL,
    login               TEXT NOT NULL,
    access_encrypted    BLOB NOT NULL,
    access_expires_at   DATETIME,
    refresh_encrypted   BLOB,
    refresh_expires_at  DATETIME,
    updated_at          DATETIME NOT NULL,
    PRIMARY KEY (workspace_id, github_user_id)
);

-- +goose Down
DROP TABLE github_user_token;
DROP TABLE github_app;
