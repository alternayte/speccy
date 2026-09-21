-- +goose Up
-- DEC-021: an external target is an issue, a page, a repo path, or a commit. implemented-by
-- names a code target: the target implements this doc. target_url is where a person opens it.
-- SQLite cannot change a CHECK, so the table is rebuilt.
ALTER TABLE link RENAME TO link_old;
DROP INDEX link_from;
DROP INDEX link_target;
CREATE TABLE link (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    from_bundle_id   UUIDTEXT NOT NULL REFERENCES bundle (id),
    kind             TEXT NOT NULL CHECK (kind IN ('implements', 'refines', 'references', 'supersedes', 'implemented-by')),
    target_kind      TEXT NOT NULL CHECK (target_kind IN ('bundle', 'external')),
    target_bundle_id UUIDTEXT REFERENCES bundle (id),
    target_ref       TEXT NOT NULL,
    origin           TEXT NOT NULL CHECK (origin IN ('frontmatter', 'rule')),
    target_url       TEXT NOT NULL DEFAULT ''
);
INSERT INTO link (id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin)
SELECT id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin FROM link_old;
DROP TABLE link_old;
CREATE INDEX link_from ON link (from_bundle_id);
CREATE INDEX link_target ON link (target_bundle_id);

-- +goose Down
ALTER TABLE link RENAME TO link_old;
DROP INDEX link_from;
DROP INDEX link_target;
CREATE TABLE link (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    from_bundle_id   UUIDTEXT NOT NULL REFERENCES bundle (id),
    kind             TEXT NOT NULL CHECK (kind IN ('implements', 'refines', 'references', 'supersedes')),
    target_kind      TEXT NOT NULL CHECK (target_kind IN ('bundle', 'external')),
    target_bundle_id UUIDTEXT REFERENCES bundle (id),
    target_ref       TEXT NOT NULL,
    origin           TEXT NOT NULL CHECK (origin IN ('frontmatter', 'rule'))
);
INSERT INTO link (id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin)
SELECT id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin
FROM link_old WHERE kind <> 'implemented-by';
DROP TABLE link_old;
CREATE INDEX link_from ON link (from_bundle_id);
CREATE INDEX link_target ON link (target_bundle_id);
