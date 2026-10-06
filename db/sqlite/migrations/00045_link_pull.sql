-- +goose Up
-- A link rule target that only an open pull request holds (#142): the pull request and the head
-- commit that the review read. pull_number 0 is a link to a doc in the tree.
ALTER TABLE link ADD COLUMN pull_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE link ADD COLUMN pull_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE link ADD COLUMN pull_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE link DROP COLUMN pull_url;
ALTER TABLE link DROP COLUMN pull_sha;
ALTER TABLE link DROP COLUMN pull_number;
