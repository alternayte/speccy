-- +goose Up
-- A tracker issue a breakdown push created is an external link of the doc, with the origin
-- breakdown (#157). SQLite cannot change a CHECK constraint, so the table is built again.
CREATE TABLE link_new (
    id                 UUIDTEXT PRIMARY KEY,
    workspace_id       UUIDTEXT NOT NULL REFERENCES workspace (id),
    from_spec_doc_id   UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    kind               TEXT NOT NULL CHECK (kind IN ('implements', 'refines', 'references', 'supersedes', 'implemented-by')),
    target_kind        TEXT NOT NULL CHECK (target_kind IN ('bundle', 'external')),
    target_spec_doc_id UUIDTEXT REFERENCES spec_doc (id),
    target_ref         TEXT NOT NULL,
    origin             TEXT NOT NULL CHECK (origin IN ('frontmatter', 'rule', 'adopted', 'breakdown')),
    target_url         TEXT NOT NULL DEFAULT '',
    pull_number        INTEGER NOT NULL DEFAULT 0,
    pull_sha           TEXT NOT NULL DEFAULT '',
    pull_url           TEXT NOT NULL DEFAULT ''
);
INSERT INTO link_new (id, workspace_id, from_spec_doc_id, kind, target_kind, target_spec_doc_id, target_ref, origin,
                      target_url, pull_number, pull_sha, pull_url)
SELECT id, workspace_id, from_spec_doc_id, kind, target_kind, target_spec_doc_id, target_ref, origin,
       target_url, pull_number, pull_sha, pull_url FROM link;
DROP TABLE link;
ALTER TABLE link_new RENAME TO link;
CREATE INDEX link_from ON link (from_spec_doc_id);
CREATE INDEX link_target ON link (target_spec_doc_id);

-- +goose Down
DELETE FROM link WHERE origin = 'breakdown';
