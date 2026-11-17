-- +goose Up
-- The breakdown of one spec doc into epics and work items (#154). One per spec doc. It moves
-- forward with the doc's versions: version_id is the version it quotes. body holds the epics,
-- the items and the coverage acknowledgements; revision guards each edit.
CREATE TABLE breakdown (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id  UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    verdict      TEXT NOT NULL,
    acknowledged BOOLEAN NOT NULL,
    revision     INTEGER NOT NULL,
    body         JSONTEXT NOT NULL,
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);
CREATE UNIQUE INDEX breakdown_spec_doc ON breakdown (spec_doc_id);

-- One tracker issue a push created for an epic or a work item (#157). A push never edits or
-- closes an issue, so a row is written once.
CREATE TABLE breakdown_issue (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id  UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    breakdown_id UUIDTEXT NOT NULL REFERENCES breakdown (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('epic', 'item')),
    item_id      TEXT NOT NULL,
    tracker      TEXT NOT NULL,
    issue_key    TEXT NOT NULL,
    issue_url    TEXT NOT NULL,
    issue_ref    TEXT NOT NULL,
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    handoff_id   UUIDTEXT NOT NULL REFERENCES handoff (id),
    trace_ids    JSONTEXT NOT NULL,
    pushed_by    TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);
CREATE UNIQUE INDEX breakdown_issue_item ON breakdown_issue (breakdown_id, kind, item_id);
CREATE INDEX breakdown_issue_key ON breakdown_issue (workspace_id, issue_key);

-- +goose Down
DROP TABLE breakdown_issue;
DROP TABLE breakdown;
