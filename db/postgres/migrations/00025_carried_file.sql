-- +goose Up
-- A carried file is in the bundle because a doc references it. carried_by names that doc.
-- Speccy renders and exports a carried file, and never writes it back.
ALTER TABLE version_file ADD COLUMN carried_by text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE version_file DROP COLUMN carried_by;
