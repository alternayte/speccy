-- +goose Up
-- SDD §11.3: event store lite.
CREATE TABLE es_streams (
    stream_id   uuid PRIMARY KEY,
    stream_type text NOT NULL,
    version     bigint NOT NULL CHECK (version > 0),
    state       jsonb NOT NULL,
    updated_at  timestamptz NOT NULL
);

CREATE TABLE es_events (
    stream_id   uuid NOT NULL REFERENCES es_streams (stream_id),
    version     bigint NOT NULL CHECK (version > 0),
    event_type  text NOT NULL,
    payload     jsonb NOT NULL,
    metadata    jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (stream_id, version)
);

-- +goose Down
DROP TABLE es_events;
DROP TABLE es_streams;
