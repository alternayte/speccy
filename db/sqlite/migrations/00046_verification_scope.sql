-- +goose Up
-- A verification run scoped to some trace IDs, or to one work item (#158). An empty scope verifies
-- every trace ID. An advisory run read a pull request head: it opens no blocking thread and does
-- not count toward the handoff.
ALTER TABLE verification_run ADD COLUMN trace_ids JSONTEXT NOT NULL DEFAULT '[]';
ALTER TABLE verification_run ADD COLUMN work_item TEXT NOT NULL DEFAULT '';
ALTER TABLE verification_run ADD COLUMN advisory BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE verification_run DROP COLUMN advisory;
ALTER TABLE verification_run DROP COLUMN work_item;
ALTER TABLE verification_run DROP COLUMN trace_ids;
