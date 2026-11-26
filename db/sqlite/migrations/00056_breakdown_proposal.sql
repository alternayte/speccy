-- +goose Up
-- A breakdown proposal that a job runs, so no proxy timeout cuts its model call
-- (docs/specs/breakdown-proposal-job.md).
CREATE TABLE breakdown_proposal (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id  UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    acknowledged BOOLEAN NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    finished_at  DATETIME
);
CREATE INDEX breakdown_proposal_doc ON breakdown_proposal (spec_doc_id, created_at);

-- +goose Down
DROP TABLE breakdown_proposal;
