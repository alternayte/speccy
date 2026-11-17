-- +goose Up
-- A verification run scoped to some trace IDs, or to one work item (#158). An empty scope verifies
-- every trace ID. An advisory run read a pull request head: it opens no blocking thread and does
-- not count toward the handoff.
ALTER TABLE verification_run ADD COLUMN trace_ids jsonb DEFAULT '[]'::jsonb NOT NULL;
ALTER TABLE verification_run ADD COLUMN work_item text DEFAULT ''::text NOT NULL;
ALTER TABLE verification_run ADD COLUMN advisory boolean DEFAULT false NOT NULL;

-- +goose Down
ALTER TABLE verification_run DROP COLUMN advisory;
ALTER TABLE verification_run DROP COLUMN work_item;
ALTER TABLE verification_run DROP COLUMN trace_ids;
