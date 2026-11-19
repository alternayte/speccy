-- +goose Up
-- The GitHub App of the workspace (docs/specs/github-app.md). A workspace holds an App or a
-- token in github_connection, never both. The secrets are sealed with SPECCY_MASTER_KEY.
CREATE TABLE github_app (
    workspace_id                uuid PRIMARY KEY REFERENCES workspace (id),
    api_url                     text NOT NULL,
    app_id                      bigint NOT NULL,
    slug                        text NOT NULL,
    owner_login                 text NOT NULL,
    owner_is_org                boolean NOT NULL,
    client_id                   text NOT NULL,
    client_secret_encrypted     bytea NOT NULL,
    private_key_encrypted       bytea NOT NULL,
    webhook_secret_encrypted    bytea NOT NULL,
    installation_id             bigint,
    created_by                  text NOT NULL,
    created_at                  timestamp with time zone NOT NULL,
    updated_at                  timestamp with time zone NOT NULL
);

-- The GitHub user token of one person, from the App's user OAuth. Doc writes go out as that
-- person. The key is the GitHub account ID, which auth-all links to the Speccy account.
CREATE TABLE github_user_token (
    workspace_id        uuid NOT NULL REFERENCES workspace (id),
    github_user_id      text NOT NULL,
    login               text NOT NULL,
    access_encrypted    bytea NOT NULL,
    access_expires_at   timestamp with time zone,
    refresh_encrypted   bytea,
    refresh_expires_at  timestamp with time zone,
    updated_at          timestamp with time zone NOT NULL,
    PRIMARY KEY (workspace_id, github_user_id)
);

-- +goose Down
DROP TABLE github_user_token;
DROP TABLE github_app;
