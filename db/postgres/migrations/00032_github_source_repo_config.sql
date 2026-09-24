-- +goose Up
-- The .speccy.yaml of a GitHub source's repo, as the last sync read it. A review of a spec doc
-- of the source reads its link patterns, link rules and adoption from it.
ALTER TABLE github_source ADD COLUMN repo_config text DEFAULT ''::text NOT NULL;

-- +goose Down
ALTER TABLE github_source DROP COLUMN repo_config;
