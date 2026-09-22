-- +goose Up
-- One verification run: one bundle version against one code repo at one SHA. The run holds
-- its own verdict. It computes no Build Ready verdict.
CREATE TABLE verification_run (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    bundle_id    uuid NOT NULL REFERENCES bundle (id) ON DELETE CASCADE,
    version_id   uuid NOT NULL REFERENCES version (id),
    handoff_id   uuid REFERENCES handoff (id),
    repo         text NOT NULL,
    sha          text NOT NULL,
    base_sha     text NOT NULL,
    digest       text NOT NULL,
    verdict      text NOT NULL,
    counts       jsonb NOT NULL,
    notes        jsonb NOT NULL DEFAULT '[]',
    stale        boolean NOT NULL DEFAULT false,
    started_by   text NOT NULL,
    created_at   timestamptz NOT NULL
);
CREATE INDEX verification_run_bundle ON verification_run (bundle_id, created_at);
CREATE INDEX verification_run_handoff ON verification_run (handoff_id);

-- One trace ID's outcome in one run, with the targets Speccy checked and the judges' quotes.
CREATE TABLE verification_outcome (
    id         uuid PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES verification_run (id) ON DELETE CASCADE,
    trace_id   text NOT NULL,
    outcome    text NOT NULL,
    level      text NOT NULL,
    blocks     boolean NOT NULL,
    waived     boolean NOT NULL,
    provenance text NOT NULL,
    note       text NOT NULL,
    targets    jsonb NOT NULL,
    judgement  jsonb NOT NULL
);
CREATE INDEX verification_outcome_run ON verification_outcome (run_id, trace_id);

-- +goose Down
DROP TABLE verification_outcome;
DROP TABLE verification_run;
