-- +goose Up
-- A person who can edit a doc withdraws an approved Acknowledgement: the waiver stream records
-- WaiverWithdrawn, and the view shows the status withdrawn. SQLite changes a CHECK only by
-- building the table again.
CREATE TABLE waiver_view_next (
    id            UUIDTEXT PRIMARY KEY,
    workspace_id  UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id   UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    check_slug    TEXT NOT NULL,
    level         TEXT NOT NULL,
    section_path  JSONTEXT NOT NULL,
    section_hash  TEXT NOT NULL,
    reason        TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('requested', 'approved', 'rejected', 'invalidated', 'withdrawn')),
    requested_by  TEXT NOT NULL,
    approvals     JSONTEXT NOT NULL,
    decided_by    TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    scope         TEXT NOT NULL DEFAULT 'check',
    trace_id      TEXT NOT NULL DEFAULT '',
    repo          TEXT NOT NULL DEFAULT ''
);
INSERT INTO waiver_view_next (id, workspace_id, spec_doc_id, check_slug, level, section_path, section_hash, reason,
    status, requested_by, approvals, decided_by, created_at, updated_at, scope, trace_id, repo)
SELECT id, workspace_id, spec_doc_id, check_slug, level, section_path, section_hash, reason,
    status, requested_by, approvals, decided_by, created_at, updated_at, scope, trace_id, repo
FROM waiver_view;
DROP TABLE waiver_view;
ALTER TABLE waiver_view_next RENAME TO waiver_view;
CREATE INDEX waiver_view_verify ON waiver_view (spec_doc_id, scope, repo, trace_id);
CREATE INDEX waiver_view_spec_doc ON waiver_view (spec_doc_id, status);

-- +goose Down
CREATE TABLE waiver_view_prev (
    id            UUIDTEXT PRIMARY KEY,
    workspace_id  UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id   UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    check_slug    TEXT NOT NULL,
    level         TEXT NOT NULL,
    section_path  JSONTEXT NOT NULL,
    section_hash  TEXT NOT NULL,
    reason        TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('requested', 'approved', 'rejected', 'invalidated')),
    requested_by  TEXT NOT NULL,
    approvals     JSONTEXT NOT NULL,
    decided_by    TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    scope         TEXT NOT NULL DEFAULT 'check',
    trace_id      TEXT NOT NULL DEFAULT '',
    repo          TEXT NOT NULL DEFAULT ''
);
INSERT INTO waiver_view_prev (id, workspace_id, spec_doc_id, check_slug, level, section_path, section_hash, reason,
    status, requested_by, approvals, decided_by, created_at, updated_at, scope, trace_id, repo)
SELECT id, workspace_id, spec_doc_id, check_slug, level, section_path, section_hash, reason,
    CASE status WHEN 'withdrawn' THEN 'invalidated' ELSE status END, requested_by, approvals, decided_by,
    created_at, updated_at, scope, trace_id, repo
FROM waiver_view;
DROP TABLE waiver_view;
ALTER TABLE waiver_view_prev RENAME TO waiver_view;
CREATE INDEX waiver_view_verify ON waiver_view (spec_doc_id, scope, repo, trace_id);
CREATE INDEX waiver_view_spec_doc ON waiver_view (spec_doc_id, status);
