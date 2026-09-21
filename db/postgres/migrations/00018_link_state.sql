-- +goose Up
-- DEC-021: the state of each external link of a bundle, as the last review run read it.
-- checked_ref is the commit of a code target, or the content hash of a fetched page.
CREATE TABLE link_state (
    bundle_id   uuid NOT NULL REFERENCES bundle (id) ON DELETE CASCADE,
    target_ref  text NOT NULL,
    state       text NOT NULL CHECK (state IN ('aligned', 'drifted', 'conflicting', 'unchecked')),
    reason      text NOT NULL,
    checked_ref text NOT NULL,
    checked_at  timestamptz NOT NULL,
    PRIMARY KEY (bundle_id, target_ref)
);

-- +goose Down
DROP TABLE link_state;
