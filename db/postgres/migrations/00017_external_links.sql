-- +goose Up
-- DEC-021: an external target is an issue, a page, a repo path, or a commit. implemented-by
-- names a code target: the target implements this doc. target_url is where a person opens it.
ALTER TABLE link DROP CONSTRAINT link_kind_check;
ALTER TABLE link ADD CONSTRAINT link_kind_check
    CHECK (kind IN ('implements', 'refines', 'references', 'supersedes', 'implemented-by'));
ALTER TABLE link ADD COLUMN target_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE link DROP COLUMN target_url;
DELETE FROM link WHERE kind = 'implemented-by';
ALTER TABLE link DROP CONSTRAINT link_kind_check;
ALTER TABLE link ADD CONSTRAINT link_kind_check
    CHECK (kind IN ('implements', 'refines', 'references', 'supersedes'));
