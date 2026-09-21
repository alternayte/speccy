-- +goose Up
-- REQ-137: a thread that a builder opened from its handoff. handoff_id names the handoff, and
-- handoff_version the bundle version the builder took, so the rail says what it built from.
ALTER TABLE thread_view ADD COLUMN handoff_id UUIDTEXT;
ALTER TABLE thread_view ADD COLUMN handoff_version BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE thread_view DROP COLUMN handoff_id;
ALTER TABLE thread_view DROP COLUMN handoff_version;
