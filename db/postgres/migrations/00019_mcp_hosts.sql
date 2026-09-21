-- +goose Up
-- DEC-021: the hosts a connection can read, and the read-only tool that reads one page. Speccy
-- matches an external link to a connection by host, so no model picks the tool.
ALTER TABLE mcp_connection ADD COLUMN hosts jsonb NOT NULL DEFAULT '[]';
ALTER TABLE mcp_connection ADD COLUMN fetch_tool text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE mcp_connection DROP COLUMN hosts;
ALTER TABLE mcp_connection DROP COLUMN fetch_tool;
