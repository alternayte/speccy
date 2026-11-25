-- +goose Up
-- A source made from a pull request URL (docs/specs/pr-source-and-github-review.md). pull is 0
-- for a branch source. docs are the repo paths of the spec docs the person picked. pull_state
-- is open, merged or closed.
ALTER TABLE github_source ADD COLUMN pull INTEGER NOT NULL DEFAULT 0;
ALTER TABLE github_source ADD COLUMN pull_title TEXT NOT NULL DEFAULT '';
ALTER TABLE github_source ADD COLUMN pull_author TEXT NOT NULL DEFAULT '';
ALTER TABLE github_source ADD COLUMN pull_author_id TEXT NOT NULL DEFAULT '';
ALTER TABLE github_source ADD COLUMN pull_state TEXT NOT NULL DEFAULT '';
ALTER TABLE github_source ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE github_source ADD COLUMN docs JSONTEXT NOT NULL DEFAULT '[]';

-- A reply on GitHub to a pull request comment that holds Speccy's key marker. The app shows it
-- on the finding or the questions item of that key.
CREATE TABLE github_reply (
    source_id   UUIDTEXT NOT NULL REFERENCES github_source (id) ON DELETE CASCADE,
    comment_id  INTEGER NOT NULL,
    spec_doc_id UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    finding_key TEXT NOT NULL,
    author      TEXT NOT NULL,
    body        TEXT NOT NULL,
    url         TEXT NOT NULL,
    created_at  DATETIME NOT NULL,
    PRIMARY KEY (source_id, comment_id)
);
CREATE INDEX github_reply_doc ON github_reply (spec_doc_id, created_at);

-- +goose Down
DROP TABLE github_reply;
ALTER TABLE github_source DROP COLUMN docs;
ALTER TABLE github_source DROP COLUMN base_branch;
ALTER TABLE github_source DROP COLUMN pull_state;
ALTER TABLE github_source DROP COLUMN pull_author_id;
ALTER TABLE github_source DROP COLUMN pull_author;
ALTER TABLE github_source DROP COLUMN pull_title;
ALTER TABLE github_source DROP COLUMN pull;
