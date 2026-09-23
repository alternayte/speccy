-- +goose Up
-- An adopted link: a link a person confirmed in Speccy for a doc in a repo source. Speccy
-- writes nothing into the repo. path is the doc that links, and target is the root-relative
-- path of the doc it links to. A link of the same kind that the repo names replaces it.
CREATE TABLE adopted_link (
    source_id UUIDTEXT NOT NULL REFERENCES github_source (id) ON DELETE CASCADE,
    path      TEXT NOT NULL,
    kind      TEXT NOT NULL,
    target    TEXT NOT NULL,
    PRIMARY KEY (source_id, path, kind)
);
-- SQLite cannot change a CHECK, so the link table is rebuilt to allow the adopted origin.
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
    origin           TEXT NOT NULL CHECK (origin IN ('frontmatter', 'rule', 'adopted')),
    target_url       TEXT NOT NULL DEFAULT ''
);
INSERT INTO link (id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin, target_url)
SELECT id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin, target_url FROM link_old;
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
    kind             TEXT NOT NULL CHECK (kind IN ('implements', 'refines', 'references', 'supersedes', 'implemented-by')),
    target_kind      TEXT NOT NULL CHECK (target_kind IN ('bundle', 'external')),
    target_bundle_id UUIDTEXT REFERENCES bundle (id),
    target_ref       TEXT NOT NULL,
    origin           TEXT NOT NULL CHECK (origin IN ('frontmatter', 'rule')),
    target_url       TEXT NOT NULL DEFAULT ''
);
INSERT INTO link (id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin, target_url)
SELECT id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin, target_url
FROM link_old WHERE origin <> 'adopted';
DROP TABLE link_old;
CREATE INDEX link_from ON link (from_bundle_id);
CREATE INDEX link_target ON link (target_bundle_id);
DROP TABLE adopted_link;
