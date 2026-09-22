-- +goose Up
-- The claim class the profile's source policy gave the section a claim is anchored in.
-- A claim in a section that matches no pattern is unclassified.
ALTER TABLE claim ADD COLUMN class TEXT NOT NULL DEFAULT 'unclassified';

-- +goose Down
ALTER TABLE claim DROP COLUMN class;
