-- +goose Up
-- A URL connector: a doc index, such as an llms.txt, on the web or in a GitHub repo, that the
-- grounding stage and external links read with no MCP server (docs/specs/url-connectors.md).
-- The header value is sealed with SPECCY_MASTER_KEY. profiles is empty for every doc.
CREATE TABLE url_connector (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspace (id),
    name             text NOT NULL,
    address          text NOT NULL,
    base_url         text NOT NULL,
    header_name      text NOT NULL DEFAULT '',
    header_encrypted bytea,
    header_last4     text NOT NULL DEFAULT '',
    profiles         jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at       timestamp with time zone NOT NULL
);

CREATE UNIQUE INDEX url_connector_name ON url_connector (workspace_id, name);

-- +goose Down
DROP TABLE url_connector;
