-- +goose Up
-- REQ-133: a markdown file a person marked as not a spec. Speccy stops offering to adopt it.
-- source_id is null for a file of the local folder.
CREATE TABLE dismissed_doc (
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    source_id    uuid REFERENCES github_source (id) ON DELETE CASCADE,
    path         text NOT NULL,
    dismissed_by text NOT NULL,
    created_at   timestamptz NOT NULL
);
CREATE UNIQUE INDEX dismissed_doc_key ON dismissed_doc (workspace_id, coalesce(source_id, '00000000-0000-0000-0000-000000000000'::uuid), path);

-- +goose Down
DROP TABLE dismissed_doc;
