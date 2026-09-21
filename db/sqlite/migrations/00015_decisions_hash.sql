-- +goose Up
-- DEC-009: the sidecar holds the waivers and acknowledgements, and changes no version of the
-- doc. A run records the sidecar it read, so a decision made after the run lints again.
ALTER TABLE review_run ADD COLUMN decisions_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE review_run DROP COLUMN decisions_hash;
