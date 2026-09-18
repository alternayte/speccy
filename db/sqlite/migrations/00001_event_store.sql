-- +goose Up
-- SDD §11.3: event store lite.
CREATE TABLE es_streams (
    stream_id   TEXT PRIMARY KEY,
    stream_type TEXT NOT NULL,
    version     INTEGER NOT NULL CHECK (version > 0),
    state       TEXT NOT NULL CHECK (json_valid(state)),
    updated_at  DATETIME NOT NULL
);

CREATE TABLE es_events (
    stream_id   TEXT NOT NULL REFERENCES es_streams (stream_id),
    version     INTEGER NOT NULL CHECK (version > 0),
    event_type  TEXT NOT NULL,
    payload     TEXT NOT NULL CHECK (json_valid(payload)),
    metadata    TEXT NOT NULL CHECK (json_valid(metadata)),
    occurred_at DATETIME NOT NULL,
    PRIMARY KEY (stream_id, version)
);

-- +goose Down
DROP TABLE es_events;
DROP TABLE es_streams;
