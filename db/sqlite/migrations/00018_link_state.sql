-- +goose Up
-- DEC-021: the state of each external link of a bundle, as the last review run read it.
-- checked_ref is the commit of a code target, or the content hash of a fetched page.
CREATE TABLE link_state (
    bundle_id   UUIDTEXT NOT NULL REFERENCES bundle (id) ON DELETE CASCADE,
    target_ref  TEXT NOT NULL,
    state       TEXT NOT NULL CHECK (state IN ('aligned', 'drifted', 'conflicting', 'unchecked')),
    reason      TEXT NOT NULL,
    checked_ref TEXT NOT NULL,
    checked_at  DATETIME NOT NULL,
    PRIMARY KEY (bundle_id, target_ref)
);

-- +goose Down
DROP TABLE link_state;
