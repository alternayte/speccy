-- +goose Up
-- One verification run: one bundle version against one code repo at one SHA. The run holds
-- its own verdict. It computes no Build Ready verdict.
CREATE TABLE verification_run (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id) ON DELETE CASCADE,
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    handoff_id   UUIDTEXT REFERENCES handoff (id),
    repo         TEXT NOT NULL,
    sha          TEXT NOT NULL,
    base_sha     TEXT NOT NULL,
    digest       TEXT NOT NULL,
    verdict      TEXT NOT NULL,
    counts       JSONTEXT NOT NULL,
    notes        JSONTEXT NOT NULL DEFAULT '[]',
    stale        BOOLEAN NOT NULL DEFAULT false,
    started_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);
CREATE INDEX verification_run_bundle ON verification_run (bundle_id, created_at);
CREATE INDEX verification_run_handoff ON verification_run (handoff_id);

-- One trace ID's outcome in one run, with the targets Speccy checked and the judges' quotes.
CREATE TABLE verification_outcome (
    id         UUIDTEXT PRIMARY KEY,
    run_id     UUIDTEXT NOT NULL REFERENCES verification_run (id) ON DELETE CASCADE,
    trace_id   TEXT NOT NULL,
    outcome    TEXT NOT NULL,
    level      TEXT NOT NULL,
    blocks     BOOLEAN NOT NULL,
    waived     BOOLEAN NOT NULL,
    provenance TEXT NOT NULL,
    note       TEXT NOT NULL,
    targets    JSONTEXT NOT NULL,
    judgement  JSONTEXT NOT NULL
);
CREATE INDEX verification_outcome_run ON verification_outcome (run_id, trace_id);

-- +goose Down
DROP TABLE verification_outcome;
DROP TABLE verification_run;
