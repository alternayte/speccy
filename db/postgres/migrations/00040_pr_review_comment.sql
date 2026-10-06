-- +goose Up
-- A pending review with no attribution has no hidden marker, so the local state keeps what
-- Speccy wrote in it: the finding key or the question ID of each comment by its GitHub node ID,
-- and the part of the review body that Speccy wrote (kind review). A later batch merges into
-- the review with these rows.
CREATE TABLE pr_review_comment (
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    repo text NOT NULL,
    pull bigint NOT NULL,
    comment_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('finding', 'ask', 'body', 'review')),
    ref text NOT NULL,
    body text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    PRIMARY KEY (workspace_id, repo, pull, comment_id, kind, ref)
);

-- The .speccy.yaml that the review of a batch pull request used.
ALTER TABLE pr_batch_item ADD COLUMN config jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE pr_batch_item DROP COLUMN config;
DROP TABLE pr_review_comment;
