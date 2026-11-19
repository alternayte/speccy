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
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL,
    attempts bigint DEFAULT 0 NOT NULL,
    locked_until timestamp with time zone,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT job_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'done'::text, 'failed'::text])))
);
CREATE INDEX job_queue ON job USING btree (status, created_at);
