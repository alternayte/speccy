-- +goose Up
-- River runs the jobs from here on (docs/specs/hosted-pr-review.md). A job left in the old
-- table has no worker any more, so its run ends now and the doc can be reviewed again.
UPDATE review_run SET status = 'failed', finished_at = CURRENT_TIMESTAMP,
    error = 'The process that ran this review stopped before the review ended. Run the review again.'
WHERE status IN ('queued', 'running');
UPDATE verification_run SET status = 'failed',
    error = 'The process that ran this verification stopped before it ended. Run the verification again.'
WHERE status IN ('queued', 'running');
DROP TABLE job;

-- +goose Down
CREATE TABLE job (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    kind         TEXT NOT NULL,
    payload      JSONTEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    locked_until DATETIME,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL
);
CREATE INDEX job_queue ON job (status, created_at);
