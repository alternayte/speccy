-- +goose Up
-- The count of Speccy's comments that a batch removed from a pending review, because their
-- finding is gone.
ALTER TABLE pr_batch_item ADD COLUMN removed bigint DEFAULT 0 NOT NULL;

-- +goose Down
ALTER TABLE pr_batch_item DROP COLUMN removed;
