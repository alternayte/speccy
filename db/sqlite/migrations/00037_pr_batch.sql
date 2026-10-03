-- +goose Up
-- A batch reviews many spec pull requests and posts a pending review on each one, as the local
-- user (docs/specs/pr-review-batch.md). pr_review records each pull request review at its head
-- commit, so a second batch skips a pull request with no new commit.
CREATE TABLE pr_batch (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    status       TEXT NOT NULL CHECK (status IN ('planned', 'running', 'done', 'cancelled', 'stopped')),
    parallel     INTEGER NOT NULL,
    stages       JSONTEXT NOT NULL CHECK (json_valid(stages)),
    again        BOOLEAN NOT NULL,
    source       TEXT NOT NULL,
    estimate     JSONTEXT NOT NULL CHECK (json_valid(estimate)),
    created_at   DATETIME NOT NULL,
    finished_at  DATETIME
);

CREATE TABLE pr_batch_item (
    batch_id   UUIDTEXT NOT NULL REFERENCES pr_batch (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    repo       TEXT NOT NULL,
    pull       INTEGER NOT NULL,
    url        TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('waiting', 'reviewing', 'posted', 'skipped', 'failed')),
    reason     TEXT NOT NULL,
    head_sha   TEXT NOT NULL,
    result     JSONTEXT NOT NULL CHECK (json_valid(result)),
    comments   INTEGER NOT NULL,
    review_url TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (batch_id, position)
);

CREATE TABLE pr_review (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    repo         TEXT NOT NULL,
    pull         INTEGER NOT NULL,
    head_sha     TEXT NOT NULL,
    batch_id     UUIDTEXT,
    reviewed_at  DATETIME NOT NULL,
    PRIMARY KEY (workspace_id, repo, pull, head_sha)
);

-- +goose Down
DROP TABLE pr_review;
DROP TABLE pr_batch_item;
DROP TABLE pr_batch;
