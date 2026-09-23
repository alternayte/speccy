-- +goose Up
-- The schema of Speccy 0.15.0, in one migration. From 0.15.0 on, Speccy keeps no data from an
-- older version, so the steps that led here are gone. A database from before 0.15.0 stops at
-- start with a message that says so (store.Migrate).
CREATE TABLE es_streams (
    stream_id   UUIDTEXT PRIMARY KEY,
    stream_type TEXT NOT NULL,
    version     INTEGER NOT NULL CHECK (version > 0),
    state       JSONTEXT NOT NULL CHECK (json_valid(state)),
    updated_at  DATETIME NOT NULL
);

CREATE TABLE es_events (
    stream_id   UUIDTEXT NOT NULL REFERENCES es_streams (stream_id),
    version     INTEGER NOT NULL CHECK (version > 0),
    event_type  TEXT NOT NULL,
    payload     JSONTEXT NOT NULL CHECK (json_valid(payload)),
    metadata    JSONTEXT NOT NULL CHECK (json_valid(metadata)),
    occurred_at DATETIME NOT NULL,
    PRIMARY KEY (stream_id, version)
);

CREATE TABLE workspace (
    id         UUIDTEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    settings   JSONTEXT NOT NULL DEFAULT '{}' CHECK (json_valid(settings)),
    created_at DATETIME NOT NULL
);

CREATE TABLE blob (
    sha256  TEXT PRIMARY KEY,
    content BLOB NOT NULL,
    size    INTEGER NOT NULL CHECK (size >= 0)
);

CREATE TABLE version (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id    UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    number       INTEGER NOT NULL CHECK (number > 0),
    created_by   TEXT NOT NULL,
    message      TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    UNIQUE (spec_doc_id, number)
);

CREATE TABLE version_file (
    version_id UUIDTEXT NOT NULL REFERENCES version (id),
    path       TEXT NOT NULL,
    sha256     TEXT NOT NULL REFERENCES blob (sha256),
    carried_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (version_id, path)
);

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
    spec_doc_id       UUIDTEXT NOT NULL REFERENCES spec_doc (id),
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
, notes JSONTEXT NOT NULL DEFAULT '[]', stages JSONTEXT NOT NULL DEFAULT '[]', decisions_hash TEXT NOT NULL DEFAULT '');

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
, waived BOOLEAN NOT NULL DEFAULT false);

CREATE TABLE verdict (
    run_id               UUIDTEXT PRIMARY KEY REFERENCES review_run (id),
    result               TEXT NOT NULL CHECK (result IN ('build_ready', 'not_build_ready')),
    score                INTEGER NOT NULL,
    radar                JSONTEXT NOT NULL,
    waiver_count         INTEGER NOT NULL,
    relaxed_count        INTEGER NOT NULL,
    blocking_finding_ids JSONTEXT NOT NULL
, items JSONTEXT NOT NULL DEFAULT '[]', carried_run_id UUIDTEXT, carried_findings JSONTEXT NOT NULL DEFAULT '[]', sections_changed INTEGER NOT NULL DEFAULT 0);

CREATE TABLE model_backend (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    kind             TEXT NOT NULL CHECK (kind IN ('openai', 'anthropic', 'openrouter', 'deepseek', 'agent_cli', 'fake')),
    name             TEXT NOT NULL,
    config           JSONTEXT NOT NULL,
    secret_encrypted BLOB,
    secret_last4     TEXT NOT NULL,
    created_at       DATETIME NOT NULL,
    UNIQUE (workspace_id, name)
);

CREATE TABLE role_assignment (
    workspace_id       UUIDTEXT NOT NULL REFERENCES workspace (id),
    role               TEXT NOT NULL CHECK (role IN ('reviewer', 'reader_1', 'reader_2', 'reader_3', 'judge', 'writer')),
    backend_id         UUIDTEXT NOT NULL REFERENCES model_backend (id),
    model              TEXT NOT NULL,
    price_in_per_mtok  REAL NOT NULL,
    price_out_per_mtok REAL NOT NULL,
    PRIMARY KEY (workspace_id, role)
);

CREATE TABLE budget (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    month        TEXT NOT NULL,
    token_limit  INTEGER,
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, month)
);

CREATE TABLE job (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    kind         TEXT NOT NULL,
    payload      JSONTEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    locked_until DATETIME,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL
);

CREATE TABLE cache_entry (
    key_hash   TEXT PRIMARY KEY,
    result     JSONTEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE mcp_connection (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    name             TEXT NOT NULL,
    transport        TEXT NOT NULL CHECK (transport IN ('stdio', 'http')),
    command_or_url   JSONTEXT NOT NULL,
    secret_encrypted BLOB,
    secret_last4     TEXT NOT NULL,
    tool_allowlist   JSONTEXT NOT NULL,
    is_search        BOOLEAN NOT NULL,
    search_tool      TEXT NOT NULL,
    created_at       DATETIME NOT NULL,
    hosts JSONTEXT NOT NULL DEFAULT '[]',
    fetch_tool TEXT NOT NULL DEFAULT '',
    UNIQUE (workspace_id, name)
);

CREATE TABLE claim (
    id         UUIDTEXT PRIMARY KEY,
    run_id     UUIDTEXT NOT NULL REFERENCES review_run (id),
    text       TEXT NOT NULL,
    label      TEXT NOT NULL CHECK (label IN ('verified', 'contradicted', 'unverified')),
    reason     TEXT NOT NULL,
    sources    JSONTEXT NOT NULL,
    anchor     JSONTEXT NOT NULL
, class TEXT NOT NULL DEFAULT 'unclassified');

CREATE TABLE question (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id    UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    number       INTEGER NOT NULL CHECK (number > 0),
    text         TEXT NOT NULL,
    level        TEXT NOT NULL CHECK (level IN ('MUST', 'SHOULD')),
    cites        JSONTEXT NOT NULL,
    anchor       JSONTEXT NOT NULL,
    input_hash TEXT NOT NULL DEFAULT '',
    UNIQUE (version_id, number)
);

CREATE TABLE answer (
    question_id       UUIDTEXT NOT NULL REFERENCES question (id),
    run_id            UUIDTEXT NOT NULL REFERENCES review_run (id),
    reader_role       TEXT NOT NULL,
    model_fingerprint TEXT NOT NULL,
    answer            TEXT NOT NULL,
    quotes            JSONTEXT NOT NULL,
    quotes_found      BOOLEAN NOT NULL,
    PRIMARY KEY (run_id, question_id, reader_role)
);

CREATE TABLE question_result (
    run_id      UUIDTEXT NOT NULL REFERENCES review_run (id),
    question_id UUIDTEXT NOT NULL REFERENCES question (id),
    result      TEXT NOT NULL CHECK (result IN ('agree', 'diverge', 'gap')),
    groups      JSONTEXT NOT NULL,
    PRIMARY KEY (run_id, question_id)
);

CREATE TABLE run_link (
    run_id     UUIDTEXT NOT NULL REFERENCES review_run (id),
    spec_doc_id  UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    version_id UUIDTEXT NOT NULL REFERENCES version (id),
    PRIMARY KEY (run_id, spec_doc_id)
);

CREATE TABLE invite (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    token_hash   TEXT NOT NULL UNIQUE,
    role         TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    expires_at   DATETIME NOT NULL,
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    used_at      DATETIME,
    used_by      TEXT,
    revoked_at   DATETIME
);

CREATE TABLE reset_link (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    token_hash   TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL,
    expires_at   DATETIME NOT NULL,
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    used_at      DATETIME
);

CREATE TABLE spec_doc_reviewer (
    spec_doc_id UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    user_id   TEXT NOT NULL,
    PRIMARY KEY (spec_doc_id, user_id)
);

CREATE TABLE thread_view (
    id              UUIDTEXT PRIMARY KEY,
    workspace_id    UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id       UUIDTEXT REFERENCES spec_doc (id),
    profile_key     TEXT NOT NULL DEFAULT '',
    anchor_kind     TEXT NOT NULL CHECK (anchor_kind IN ('text', 'section', 'finding', 'check')),
    anchor          JSONTEXT NOT NULL,
    addressed_to    TEXT NOT NULL CHECK (addressed_to IN ('humans', 'ai')),
    title           TEXT NOT NULL,
    blocking        BOOLEAN NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
    created_by      TEXT NOT NULL,
    created_at      DATETIME NOT NULL,
    last_message_at DATETIME NOT NULL,
    message_count   INTEGER NOT NULL
, handoff_id UUIDTEXT, handoff_version BIGINT NOT NULL DEFAULT 0);

CREATE TABLE thread_message_view (
    id          UUIDTEXT PRIMARY KEY,
    thread_id   UUIDTEXT NOT NULL REFERENCES thread_view (id),
    seq         INTEGER NOT NULL,
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'guest', 'ai')),
    author_id   TEXT NOT NULL,
    author_name TEXT NOT NULL,
    body        TEXT NOT NULL,
    sources     JSONTEXT NOT NULL,
    decision    TEXT NOT NULL CHECK (decision IN ('', 'decision', 'reversal')),
    created_at  DATETIME NOT NULL,
    UNIQUE (thread_id, seq)
);

CREATE TABLE waiver_view (
    id            UUIDTEXT PRIMARY KEY,
    workspace_id  UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id     UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    check_slug    TEXT NOT NULL,
    level         TEXT NOT NULL,
    section_path  JSONTEXT NOT NULL,
    section_hash  TEXT NOT NULL,
    reason        TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('requested', 'approved', 'rejected', 'invalidated')),
    requested_by  TEXT NOT NULL,
    approvals     JSONTEXT NOT NULL,
    decided_by    TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL
, scope TEXT NOT NULL DEFAULT 'check', trace_id TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '');

CREATE TABLE spec_doc_status_view (
    spec_doc_id        UUIDTEXT PRIMARY KEY REFERENCES spec_doc (id),
    status           TEXT NOT NULL CHECK (status IN ('draft', 'in_review', 'approved', 'superseded')),
    approvals        JSONTEXT NOT NULL,
    approved_version UUIDTEXT,
    review_requested_at DATETIME,
    approved_at      DATETIME,
    updated_at       DATETIME NOT NULL
);

CREATE TABLE profile_maintainer (
    profile_id UUIDTEXT NOT NULL REFERENCES profile (id),
    user_id    TEXT NOT NULL,
    PRIMARY KEY (profile_id, user_id)
);

CREATE TABLE user_state (
    user_id       TEXT PRIMARY KEY,
    inbox_seen_at DATETIME NOT NULL
);

CREATE TABLE github_connection (
    workspace_id    UUIDTEXT PRIMARY KEY REFERENCES workspace (id),
    token_encrypted BLOB NOT NULL,
    token_last4     TEXT NOT NULL,
    api_url         TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    updated_at      DATETIME NOT NULL
);

CREATE TABLE github_source (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    repo         TEXT NOT NULL,
    branch       TEXT NOT NULL,
    path         TEXT NOT NULL,
    head_commit  TEXT NOT NULL DEFAULT '',
    synced_at    DATETIME,
    error        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    api_url TEXT NOT NULL DEFAULT '',
    skipped JSONTEXT NOT NULL DEFAULT '[]',
    UNIQUE (workspace_id, repo, branch, path)
);

CREATE TABLE content_review (
    id              UUIDTEXT PRIMARY KEY,
    workspace_id    UUIDTEXT NOT NULL REFERENCES workspace (id),
    slug            TEXT NOT NULL,
    title           TEXT NOT NULL,
    main_doc        TEXT NOT NULL,
    profile_key     TEXT NOT NULL,
    profile_version INTEGER NOT NULL,
    files           JSONTEXT NOT NULL,
    result          JSONTEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      DATETIME NOT NULL
);

CREATE TABLE handoff (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id    UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    verdict      TEXT NOT NULL,
    acknowledged BOOLEAN NOT NULL,
    label        TEXT NOT NULL,
    taken_by     TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);

CREATE TABLE link_state (
    spec_doc_id   UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
    target_ref  TEXT NOT NULL,
    state       TEXT NOT NULL CHECK (state IN ('aligned', 'drifted', 'conflicting', 'unchecked')),
    reason      TEXT NOT NULL,
    checked_ref TEXT NOT NULL,
    checked_at  DATETIME NOT NULL,
    PRIMARY KEY (spec_doc_id, target_ref)
);

CREATE TABLE adopted_type (
    source_id UUIDTEXT NOT NULL REFERENCES github_source (id) ON DELETE CASCADE,
    path      TEXT NOT NULL,
    profile   TEXT NOT NULL,
    PRIMARY KEY (source_id, path)
);

CREATE TABLE dismissed_doc (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    source_id    UUIDTEXT REFERENCES github_source (id) ON DELETE CASCADE,
    path         TEXT NOT NULL,
    dismissed_by TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);

CREATE TABLE verification_run (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    spec_doc_id    UUIDTEXT NOT NULL REFERENCES spec_doc (id) ON DELETE CASCADE,
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
, status TEXT NOT NULL DEFAULT 'done', error TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '');

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

CREATE TABLE adopted_link (
    source_id UUIDTEXT NOT NULL REFERENCES github_source (id) ON DELETE CASCADE,
    path      TEXT NOT NULL,
    kind      TEXT NOT NULL,
    target    TEXT NOT NULL,
    PRIMARY KEY (source_id, path, kind)
);

CREATE TABLE link (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    from_spec_doc_id   UUIDTEXT NOT NULL REFERENCES spec_doc (id),
    kind             TEXT NOT NULL CHECK (kind IN ('implements', 'refines', 'references', 'supersedes', 'implemented-by')),
    target_kind      TEXT NOT NULL CHECK (target_kind IN ('bundle', 'external')),
    target_spec_doc_id UUIDTEXT REFERENCES spec_doc (id),
    target_ref       TEXT NOT NULL,
    origin           TEXT NOT NULL CHECK (origin IN ('frontmatter', 'rule', 'adopted')),
    target_url       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE inbox_read (
    user_id  TEXT NOT NULL,
    item_key TEXT NOT NULL,
    read_at  DATETIME NOT NULL,
    PRIMARY KEY (user_id, item_key)
);

CREATE TABLE bundle (
    id               UUIDTEXT PRIMARY KEY,
    workspace_id     UUIDTEXT NOT NULL REFERENCES workspace (id),
    slug             TEXT NOT NULL,
    title            TEXT NOT NULL,
    source_kind      TEXT NOT NULL CHECK (source_kind IN ('local', 'db', 'github')),
    source_ref       JSONTEXT NOT NULL DEFAULT '{}' CHECK (json_valid(source_ref)),
    visibility       TEXT NOT NULL DEFAULT 'internal' CHECK (visibility IN ('private', 'internal', 'link')),
    share_token_hash TEXT,
    share_expires_at DATETIME,
    archived_at      DATETIME,
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL,
    UNIQUE (workspace_id, slug)
);

CREATE TABLE spec_doc (
    id                 UUIDTEXT PRIMARY KEY,
    workspace_id       UUIDTEXT NOT NULL REFERENCES workspace (id),
    slug               TEXT NOT NULL,
    title              TEXT NOT NULL,
    profile_key        TEXT NOT NULL,
    doc_path           TEXT NOT NULL,
    source_kind        TEXT NOT NULL CHECK (source_kind IN ('local', 'db', 'github')),
    source_ref         JSONTEXT NOT NULL DEFAULT '{}' CHECK (json_valid(source_ref)),
    current_version_id UUIDTEXT REFERENCES version (id),
    archived_at        DATETIME,
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    bundle_id          UUIDTEXT NOT NULL REFERENCES bundle (id),
    UNIQUE (workspace_id, slug)
);

CREATE TABLE bundle_author (
    bundle_id UUIDTEXT NOT NULL REFERENCES bundle (id),
    user_id   TEXT NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);

CREATE TABLE share_guest (
    id           UUIDTEXT PRIMARY KEY,
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id),
    display_name TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);

CREATE INDEX finding_run ON finding (run_id);

CREATE INDEX job_queue ON job (status, created_at);

CREATE INDEX claim_run ON claim (run_id);

CREATE INDEX thread_view_profile ON thread_view (workspace_id, profile_key);

CREATE INDEX content_review_created ON content_review (workspace_id, created_at);

CREATE UNIQUE INDEX dismissed_doc_key ON dismissed_doc (workspace_id, coalesce(source_id, '00000000-0000-0000-0000-000000000000'), path);

CREATE INDEX verification_run_handoff ON verification_run (handoff_id);

CREATE INDEX verification_outcome_run ON verification_outcome (run_id, trace_id);

CREATE INDEX waiver_view_verify ON waiver_view (spec_doc_id, scope, repo, trace_id);

CREATE INDEX link_from ON link (from_spec_doc_id);

CREATE INDEX link_target ON link (target_spec_doc_id);

CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);

CREATE INDEX spec_doc_bundle ON spec_doc (bundle_id);

CREATE INDEX review_run_spec_doc ON review_run (spec_doc_id, started_at DESC);

CREATE INDEX thread_view_spec_doc ON thread_view (spec_doc_id, status);

CREATE INDEX waiver_view_spec_doc ON waiver_view (spec_doc_id, status);

CREATE INDEX handoff_spec_doc ON handoff (spec_doc_id, created_at);

CREATE INDEX verification_run_spec_doc ON verification_run (spec_doc_id, created_at);

-- +goose Down
DROP TABLE share_guest;
DROP TABLE bundle_author;
DROP TABLE spec_doc;
DROP TABLE bundle;
DROP TABLE inbox_read;
DROP TABLE link;
DROP TABLE adopted_link;
DROP TABLE verification_outcome;
DROP TABLE verification_run;
DROP TABLE dismissed_doc;
DROP TABLE adopted_type;
DROP TABLE link_state;
DROP TABLE handoff;
DROP TABLE content_review;
DROP TABLE github_source;
DROP TABLE github_connection;
DROP TABLE user_state;
DROP TABLE profile_maintainer;
DROP TABLE spec_doc_status_view;
DROP TABLE waiver_view;
DROP TABLE thread_message_view;
DROP TABLE thread_view;
DROP TABLE spec_doc_reviewer;
DROP TABLE reset_link;
DROP TABLE invite;
DROP TABLE run_link;
DROP TABLE question_result;
DROP TABLE answer;
DROP TABLE question;
DROP TABLE claim;
DROP TABLE mcp_connection;
DROP TABLE cache_entry;
DROP TABLE job;
DROP TABLE budget;
DROP TABLE role_assignment;
DROP TABLE model_backend;
DROP TABLE verdict;
DROP TABLE finding;
DROP TABLE review_run;
DROP TABLE profile_version;
DROP TABLE profile;
DROP TABLE version_file;
DROP TABLE version;
DROP TABLE blob;
DROP TABLE workspace;
DROP TABLE es_events;
DROP TABLE es_streams;
