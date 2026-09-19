-- +goose Up
-- The run report (SDD §13.1): when each stage started and ended.
ALTER TABLE review_run ADD COLUMN stages jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE review_run DROP COLUMN stages;
