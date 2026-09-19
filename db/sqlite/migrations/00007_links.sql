-- +goose Up
-- REQ-050, REQ-132, DEC-021: the links of each bundle's current version, from frontmatter
-- and link rules. A bundle target that no bundle matches has no target_bundle_id.
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
CREATE INDEX link_from ON link (from_bundle_id);
CREATE INDEX link_target ON link (target_bundle_id);

-- REQ-056, §8.6 rule 4: the linked bundle versions a run used.
CREATE TABLE run_link (
    run_id     UUIDTEXT NOT NULL REFERENCES review_run (id),
    bundle_id  UUIDTEXT NOT NULL REFERENCES bundle (id),
    version_id UUIDTEXT NOT NULL REFERENCES version (id),
    PRIMARY KEY (run_id, bundle_id)
);

-- +goose Down
DROP TABLE run_link;
DROP TABLE link;
