-- +goose Up
-- The hash of the parts of .speccy.yaml that apply to the doc of a run. A change to them lints
-- the doc again, although the doc has no new version (#137).
ALTER TABLE review_run ADD COLUMN config_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE review_run DROP COLUMN config_hash;
