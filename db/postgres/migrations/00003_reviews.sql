-- +goose Up
-- REQ-012: profiles are versioned; each run records the version it used.
CREATE TABLE profile (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspace (id),
    key             text NOT NULL,
    name            text NOT NULL,
    current_version bigint NOT NULL CHECK (current_version > 0),
    UNIQUE (workspace_id, key)
);

CREATE TABLE profile_version (
    profile_id uuid NOT NULL REFERENCES profile (id),
    version    bigint NOT NULL CHECK (version > 0),
    yaml       text NOT NULL,
    template   text NOT NULL,
    origin     text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (profile_id, version)
);

-- REQ-022: a run records what it reviewed and with what.
CREATE TABLE review_run (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspace (id),
    bundle_id       uuid NOT NULL REFERENCES bundle (id),
    version_id      uuid NOT NULL REFERENCES version (id),
    profile_key     text NOT NULL,
    profile_version bigint NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('lint', 'full')),
    status          text NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    stage           text NOT NULL,
    roles           jsonb NOT NULL DEFAULT '{}',
    prompt_versions jsonb NOT NULL DEFAULT '{}',
    tokens_in       bigint NOT NULL DEFAULT 0,
    tokens_out      bigint NOT NULL DEFAULT 0,
    cost_estimate   double precision NOT NULL DEFAULT 0,
    cache_hits      bigint NOT NULL DEFAULT 0,
    error           text NOT NULL DEFAULT '',
    started_at      timestamptz NOT NULL,
    finished_at     timestamptz
);
CREATE INDEX review_run_bundle ON review_run (bundle_id, started_at DESC);

CREATE TABLE finding (
    id         uuid PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES review_run (id),
    check_slug text NOT NULL,
    level      text NOT NULL CHECK (level IN ('MUST', 'SHOULD', 'INFO')),
    stage      text NOT NULL,
    relaxed    boolean NOT NULL,
    anchor     jsonb NOT NULL,
    message    text NOT NULL,
    evidence   jsonb NOT NULL DEFAULT '{}',
    suggestion jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX finding_run ON finding (run_id);

CREATE TABLE verdict (
    run_id               uuid PRIMARY KEY REFERENCES review_run (id),
    result               text NOT NULL CHECK (result IN ('build_ready', 'not_build_ready')),
    score                bigint NOT NULL,
    radar                jsonb NOT NULL,
    waiver_count         bigint NOT NULL,
    relaxed_count        bigint NOT NULL,
    blocking_finding_ids jsonb NOT NULL
);

-- +goose Down
DROP TABLE verdict;
DROP TABLE finding;
DROP TABLE review_run;
DROP TABLE profile_version;
DROP TABLE profile;
