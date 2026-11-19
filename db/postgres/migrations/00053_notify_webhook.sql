-- +goose Up
-- The workspace's one Slack or Teams channel webhook (docs/specs/channel-notifications.md).
-- The URL is a secret, sealed with SPECCY_MASTER_KEY.
CREATE TABLE notify_webhook (
    workspace_id  uuid PRIMARY KEY REFERENCES workspace (id),
    kind          text NOT NULL CHECK (kind IN ('slack', 'teams')),
    url_encrypted bytea NOT NULL,
    url_last4     text NOT NULL,
    updated_at    timestamp with time zone NOT NULL
);

-- +goose Down
DROP TABLE notify_webhook;
