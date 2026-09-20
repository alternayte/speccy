-- +goose Up
-- REQ-136: one row per handoff of a bundle to a builder: the version it took, and the verdict
-- at that moment. Speccy never runs the build, so the row has no finished state.
CREATE TABLE handoff (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id) ON DELETE CASCADE,
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    verdict      TEXT NOT NULL,
    acknowledged BOOLEAN NOT NULL,
    label        TEXT NOT NULL,
    taken_by     TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);
CREATE INDEX handoff_bundle ON handoff (bundle_id, created_at);

-- +goose Down
DROP TABLE handoff;
