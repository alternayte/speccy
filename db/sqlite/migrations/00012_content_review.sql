-- +goose Up
-- SDD §12.4: a review of files that are not saved (POST /reviews, connected mode) keeps its
-- files and result, so the Action can link to its report. Rows older than 90 days are removed.
CREATE TABLE content_review (
    id              UUIDTEXT PRIMARY KEY,
    workspace_id    UUIDTEXT NOT NULL REFERENCES workspace (id),
    slug            TEXT NOT NULL,
    title           TEXT NOT NULL,
    main_doc        TEXT NOT NULL,
    profile_key     TEXT NOT NULL,
    profile_version INTEGER NOT NULL,
    files           JSONTEXT NOT NULL,
    result          JSONTEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      DATETIME NOT NULL
);
CREATE INDEX content_review_created ON content_review (workspace_id, created_at);

-- +goose Down
DROP TABLE content_review;
