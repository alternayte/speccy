-- +goose Up
-- SDD §12.4: a review of files that are not saved (POST /reviews, connected mode) keeps its
-- files and result, so the Action can link to its report. Rows older than 90 days are removed.
CREATE TABLE content_review (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspace (id),
    slug            text NOT NULL,
    title           text NOT NULL,
    main_doc        text NOT NULL,
    profile_key     text NOT NULL,
    profile_version bigint NOT NULL,
    files           jsonb NOT NULL,
    result          jsonb NOT NULL,
    created_by      text NOT NULL,
    created_at      timestamptz NOT NULL
);
CREATE INDEX content_review_created ON content_review (workspace_id, created_at);

-- +goose Down
DROP TABLE content_review;
