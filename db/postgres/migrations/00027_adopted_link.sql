-- +goose Up
-- An adopted link: a link a person confirmed in Speccy for a doc in a repo source. Speccy
-- writes nothing into the repo. path is the doc that links, and target is the root-relative
-- path of the doc it links to. A link of the same kind that the repo names replaces it.
CREATE TABLE adopted_link (
    source_id uuid NOT NULL REFERENCES github_source (id) ON DELETE CASCADE,
    path      text NOT NULL,
    kind      text NOT NULL,
    target    text NOT NULL,
    PRIMARY KEY (source_id, path, kind)
);
ALTER TABLE link DROP CONSTRAINT link_origin_check;
ALTER TABLE link ADD CONSTRAINT link_origin_check CHECK (origin IN ('frontmatter', 'rule', 'adopted'));

-- +goose Down
DELETE FROM link WHERE origin = 'adopted';
ALTER TABLE link DROP CONSTRAINT link_origin_check;
ALTER TABLE link ADD CONSTRAINT link_origin_check CHECK (origin IN ('frontmatter', 'rule'));
DROP TABLE adopted_link;
