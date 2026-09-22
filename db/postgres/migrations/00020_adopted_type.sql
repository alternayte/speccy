-- +goose Up
-- REQ-133: the doc type a person accepted in Speccy for one path in a source, when the repo
-- names none. The repo wins: a frontmatter type or a .speccy.yaml mapping drops the row.
CREATE TABLE adopted_type (
    source_id uuid NOT NULL REFERENCES github_source (id) ON DELETE CASCADE,
    path      text NOT NULL,
    profile   text NOT NULL,
    PRIMARY KEY (source_id, path)
);

-- The markdown files the last scan passed over, so the app lists them without a second read.
ALTER TABLE github_source ADD COLUMN skipped jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE github_source DROP COLUMN skipped;
DROP TABLE adopted_type;
