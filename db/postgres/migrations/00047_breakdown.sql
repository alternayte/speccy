-- +goose Up
-- The breakdown of one spec doc into epics and work items (#154). One per spec doc. It moves
-- forward with the doc's versions: version_id is the version it quotes. body holds the epics,
-- the items and the coverage acknowledgements; revision guards each edit.
CREATE TABLE breakdown (
    id uuid NOT NULL PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace(id),
    spec_doc_id uuid NOT NULL REFERENCES spec_doc(id) ON DELETE CASCADE,
    version_id uuid NOT NULL REFERENCES version(id),
    verdict text NOT NULL,
    acknowledged boolean NOT NULL,
    revision bigint NOT NULL,
    body jsonb NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX breakdown_spec_doc ON breakdown USING btree (spec_doc_id);

-- One tracker issue a push created for an epic or a work item (#157). A push never edits or
-- closes an issue, so a row is written once.
CREATE TABLE breakdown_issue (
    id uuid NOT NULL PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace(id),
    spec_doc_id uuid NOT NULL REFERENCES spec_doc(id) ON DELETE CASCADE,
    breakdown_id uuid NOT NULL REFERENCES breakdown(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('epic', 'item')),
    item_id text NOT NULL,
    tracker text NOT NULL,
    issue_key text NOT NULL,
    issue_url text NOT NULL,
    issue_ref text NOT NULL,
    version_id uuid NOT NULL REFERENCES version(id),
    handoff_id uuid NOT NULL REFERENCES handoff(id),
    trace_ids jsonb NOT NULL,
    pushed_by text NOT NULL,
    created_at timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX breakdown_issue_item ON breakdown_issue USING btree (breakdown_id, kind, item_id);
CREATE INDEX breakdown_issue_key ON breakdown_issue USING btree (workspace_id, issue_key);

-- +goose Down
DROP TABLE breakdown_issue;
DROP TABLE breakdown;
