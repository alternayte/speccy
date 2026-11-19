-- +goose Up
-- A review that POST /reviews or POST /reviews/url asked for. A job runs it, and the caller
-- polls GET /reviews/{id} (docs/specs/hosted-pr-review.md).
CREATE TABLE review_request (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    kind         TEXT NOT NULL CHECK (kind IN ('content', 'url')),
    request      JSONTEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    result       JSONTEXT NOT NULL DEFAULT 'null',
    error        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    finished_at  DATETIME
);
CREATE INDEX review_request_created ON review_request (workspace_id, created_at);

-- +goose Down
DROP TABLE review_request;
