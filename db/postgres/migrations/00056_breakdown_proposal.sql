-- +goose Up
-- A breakdown proposal that a job runs, so no proxy timeout cuts its model call
-- (docs/specs/breakdown-proposal-job.md).
CREATE TABLE breakdown_proposal (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    spec_doc_id  uuid NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    status       text NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    acknowledged boolean NOT NULL,
    error        text NOT NULL DEFAULT '',
    created_by   text NOT NULL,
    created_at   timestamp with time zone NOT NULL,
    finished_at  timestamp with time zone
);
CREATE INDEX breakdown_proposal_doc ON breakdown_proposal (spec_doc_id, created_at);

-- +goose Down
DROP TABLE breakdown_proposal;
