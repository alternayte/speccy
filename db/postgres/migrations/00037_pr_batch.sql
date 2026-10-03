-- +goose Up
-- A batch reviews many spec pull requests and posts a pending review on each one, as the local
-- user (docs/specs/pr-review-batch.md). pr_review records each pull request review at its head
-- commit, so a second batch skips a pull request with no new commit.
CREATE TABLE pr_batch (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    status text NOT NULL CHECK (status IN ('planned', 'running', 'done', 'cancelled', 'stopped')),
    parallel bigint NOT NULL,
    stages jsonb NOT NULL,
    again boolean NOT NULL,
    source text NOT NULL,
    estimate jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone
);

CREATE TABLE pr_batch_item (
    batch_id uuid NOT NULL REFERENCES pr_batch (id) ON DELETE CASCADE,
    position bigint NOT NULL,
    repo text NOT NULL,
    pull bigint NOT NULL,
    url text NOT NULL,
    state text NOT NULL CHECK (state IN ('waiting', 'reviewing', 'posted', 'skipped', 'failed')),
    reason text NOT NULL,
    head_sha text NOT NULL,
    result jsonb NOT NULL,
    comments bigint NOT NULL,
    review_url text NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    PRIMARY KEY (batch_id, position)
);

CREATE TABLE pr_review (
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    repo text NOT NULL,
    pull bigint NOT NULL,
    head_sha text NOT NULL,
    batch_id uuid,
    reviewed_at timestamp with time zone NOT NULL,
    PRIMARY KEY (workspace_id, repo, pull, head_sha)
);

-- +goose Down
DROP TABLE pr_review;
DROP TABLE pr_batch_item;
DROP TABLE pr_batch;
