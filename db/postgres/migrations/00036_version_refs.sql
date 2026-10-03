-- +goose Up
-- The links of a version's markdown that point outside its bundle folder, and what the scan
-- did with each one: carried it, found a spec doc of another bundle, or refused a file that git
-- ignores (docs/specs/carry-repo-files.md).
ALTER TABLE version ADD COLUMN refs jsonb DEFAULT '[]'::jsonb NOT NULL;

-- +goose Down
ALTER TABLE version DROP COLUMN refs;
