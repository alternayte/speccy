-- +goose Up
CREATE TABLE profile (
    id              UUIDTEXT PRIMARY KEY,
    workspace_id    UUIDTEXT NOT NULL REFERENCES workspace (id),
    key             TEXT NOT NULL,
    name            TEXT NOT NULL,
    current_version INTEGER NOT NULL CHECK (current_version > 0),
    UNIQUE (workspace_id, key)
);

CREATE TABLE profile_version (
    profile_id UUIDTEXT NOT NULL REFERENCES profile (id),
    version    INTEGER NOT NULL CHECK (version > 0),
    yaml       TEXT NOT NULL,
    template   TEXT NOT NULL,
    origin     TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (profile_id, version)
);

CREATE TABLE review_run (
    id              UUIDTEXT PRIMARY KEY,
    workspace_id    UUIDTEXT NOT NULL REFERENCES workspace (id),
    bundle_id       UUIDTEXT NOT NULL REFERENCES bundle (id),
    version_id      UUIDTEXT NOT NULL REFERENCES version (id),
    profile_key     TEXT NOT NULL,
    profile_version INTEGER NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('lint', 'full')),
    status          TEXT NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    stage           TEXT NOT NULL,
    roles           JSONTEXT NOT NULL DEFAULT '{}',
    prompt_versions JSONTEXT NOT NULL DEFAULT '{}',
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    cost_estimate   REAL NOT NULL DEFAULT 0,
    cache_hits      INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    started_at      DATETIME NOT NULL,
    finished_at     DATETIME
);
CREATE INDEX review_run_bundle ON review_run (bundle_id, started_at DESC);

CREATE TABLE finding (
    id         UUIDTEXT PRIMARY KEY,
    run_id     UUIDTEXT NOT NULL REFERENCES review_run (id),
    check_slug TEXT NOT NULL,
    level      TEXT NOT NULL CHECK (level IN ('MUST', 'SHOULD', 'INFO')),
    stage      TEXT NOT NULL,
    relaxed    BOOLEAN NOT NULL,
    anchor     JSONTEXT NOT NULL,
    message    TEXT NOT NULL,
    evidence   JSONTEXT NOT NULL DEFAULT '{}',
    suggestion JSONTEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX finding_run ON finding (run_id);

CREATE TABLE verdict (
    run_id               UUIDTEXT PRIMARY KEY REFERENCES review_run (id),
    result               TEXT NOT NULL CHECK (result IN ('build_ready', 'not_build_ready')),
    score                INTEGER NOT NULL,
    radar                JSONTEXT NOT NULL,
    waiver_count         INTEGER NOT NULL,
    relaxed_count        INTEGER NOT NULL,
    blocking_finding_ids JSONTEXT NOT NULL
);

-- +goose Down
DROP TABLE verdict;
DROP TABLE finding;
DROP TABLE review_run;
DROP TABLE profile_version;
DROP TABLE profile;
