-- +goose Up
-- REQ-136: one row per handoff of a bundle to a builder: the version it took, and the verdict
-- at that moment. Speccy never runs the build, so the row has no finished state.
CREATE TABLE handoff (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    bundle_id    uuid NOT NULL REFERENCES bundle (id) ON DELETE CASCADE,
    version_id   uuid NOT NULL REFERENCES version (id),
    verdict      text NOT NULL,
    acknowledged boolean NOT NULL,
    label        text NOT NULL,
    taken_by     text NOT NULL,
    created_at   timestamptz NOT NULL
);
CREATE INDEX handoff_bundle ON handoff (bundle_id, created_at);

-- +goose Down
DROP TABLE handoff;
