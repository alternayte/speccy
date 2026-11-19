-- +goose Up
-- The workspace's one Slack or Teams channel webhook (docs/specs/channel-notifications.md).
-- The URL is a secret, sealed with the key of the state.
CREATE TABLE notify_webhook (
    workspace_id  UUIDTEXT PRIMARY KEY REFERENCES workspace (id),
    kind          TEXT NOT NULL CHECK (kind IN ('slack', 'teams')),
    url_encrypted BLOB NOT NULL,
    url_last4     TEXT NOT NULL,
    updated_at    DATETIME NOT NULL
);

-- +goose Down
DROP TABLE notify_webhook;
