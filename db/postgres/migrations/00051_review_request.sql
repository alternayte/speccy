-- +goose Up
-- A review that POST /reviews or POST /reviews/url asked for. A job runs it, and the caller
-- polls GET /reviews/{id} (docs/specs/hosted-pr-review.md).
CREATE TABLE review_request (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    kind         text NOT NULL CHECK (kind IN ('content', 'url')),
    request      jsonb NOT NULL,
    status       text NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    result       jsonb NOT NULL DEFAULT 'null'::jsonb,
    error        text NOT NULL DEFAULT '',
    created_by   text NOT NULL,
    created_at   timestamp with time zone NOT NULL,
    finished_at  timestamp with time zone
);
CREATE INDEX review_request_created ON review_request (workspace_id, created_at);

-- +goose Down
DROP TABLE review_request;
