-- +goose Up
-- A URL connector: a doc index, such as an llms.txt, on the web or in a GitHub repo, that the
-- grounding stage and external links read with no MCP server (docs/specs/url-connectors.md).
-- The header value is sealed with the key of the state. profiles is empty for every doc.
CREATE TABLE url_connector (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    name             TEXT NOT NULL,
    address          TEXT NOT NULL,
    base_url         TEXT NOT NULL,
    header_name      TEXT NOT NULL DEFAULT '',
    header_encrypted BLOB,
    header_last4     TEXT NOT NULL DEFAULT '',
    profiles         JSONTEXT NOT NULL DEFAULT '[]',
    created_at       DATETIME NOT NULL
);

CREATE UNIQUE INDEX url_connector_name ON url_connector (workspace_id, name);

-- +goose Down
DROP TABLE url_connector;
