-- +goose Up
-- A person asks for a fresh review of a doc (docs/specs/findings-stay-until-fixed.md). A full
-- review after that time judges each rubric check from nothing: it does not ask about the
-- shortfalls of a review before it.
ALTER TABLE spec_doc ADD COLUMN fresh_at timestamptz;

-- +goose Down
ALTER TABLE spec_doc DROP COLUMN fresh_at;
