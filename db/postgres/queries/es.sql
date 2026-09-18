-- name: InsertStream :execrows
INSERT INTO es_streams (stream_id, stream_type, version, state, updated_at)
VALUES (sqlc.arg(stream_id), sqlc.arg(stream_type), sqlc.arg(version), sqlc.arg(state), sqlc.arg(updated_at))
ON CONFLICT (stream_id) DO NOTHING;

-- name: UpdateStream :execrows
UPDATE es_streams
SET version = sqlc.arg(version), state = sqlc.arg(state), updated_at = sqlc.arg(updated_at)
WHERE stream_id = sqlc.arg(stream_id) AND stream_type = sqlc.arg(stream_type) AND version = sqlc.arg(expected_version);

-- name: InsertEvent :exec
INSERT INTO es_events (stream_id, version, event_type, payload, metadata, occurred_at)
VALUES (sqlc.arg(stream_id), sqlc.arg(version), sqlc.arg(event_type), sqlc.arg(payload), sqlc.arg(metadata), sqlc.arg(occurred_at));

-- name: GetStream :one
SELECT stream_id, stream_type, version, state, updated_at
FROM es_streams
WHERE stream_id = sqlc.arg(stream_id);

-- name: ListEvents :many
SELECT stream_id, version, event_type, payload, metadata, occurred_at
FROM es_events
WHERE stream_id = sqlc.arg(stream_id)
ORDER BY version;
