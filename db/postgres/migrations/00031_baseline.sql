-- +goose Up
-- The schema of Speccy 0.15.0, in one migration. From 0.15.0 on, Speccy keeps no data from an
-- older version, so the steps that led here are gone. A database from before 0.15.0 stops at
-- start with a message that says so (store.Migrate).
CREATE TABLE adopted_link (
    source_id uuid NOT NULL,
    path text NOT NULL,
    kind text NOT NULL,
    target text NOT NULL
);

CREATE TABLE adopted_type (
    source_id uuid NOT NULL,
    path text NOT NULL,
    profile text NOT NULL
);

CREATE TABLE answer (
    question_id uuid NOT NULL,
    run_id uuid NOT NULL,
    reader_role text NOT NULL,
    model_fingerprint text NOT NULL,
    answer text NOT NULL,
    quotes jsonb NOT NULL,
    quotes_found boolean NOT NULL
);

CREATE TABLE blob (
    sha256 text NOT NULL,
    content bytea NOT NULL,
    size bigint NOT NULL,
    CONSTRAINT blob_size_check CHECK ((size >= 0))
);

CREATE TABLE budget (
    workspace_id uuid NOT NULL,
    month text NOT NULL,
    token_limit bigint,
    tokens_used bigint DEFAULT 0 NOT NULL
);

CREATE TABLE bundle (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    slug text NOT NULL,
    title text NOT NULL,
    source_kind text NOT NULL,
    source_ref jsonb DEFAULT '{}'::jsonb NOT NULL,
    visibility text DEFAULT 'internal'::text NOT NULL,
    share_token_hash text,
    share_expires_at timestamp with time zone,
    archived_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT bundle_source_kind_check1 CHECK ((source_kind = ANY (ARRAY['local'::text, 'db'::text, 'github'::text]))),
    CONSTRAINT bundle_visibility_check1 CHECK ((visibility = ANY (ARRAY['private'::text, 'internal'::text, 'link'::text])))
);

CREATE TABLE bundle_author (
    bundle_id uuid NOT NULL,
    user_id text NOT NULL
);

CREATE TABLE cache_entry (
    key_hash text NOT NULL,
    result jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE claim (
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    text text NOT NULL,
    label text NOT NULL,
    reason text NOT NULL,
    sources jsonb NOT NULL,
    anchor jsonb NOT NULL,
    class text DEFAULT 'unclassified'::text NOT NULL,
    CONSTRAINT claim_label_check CHECK ((label = ANY (ARRAY['verified'::text, 'contradicted'::text, 'unverified'::text])))
);

CREATE TABLE content_review (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    slug text NOT NULL,
    title text NOT NULL,
    main_doc text NOT NULL,
    profile_key text NOT NULL,
    profile_version bigint NOT NULL,
    files jsonb NOT NULL,
    result jsonb NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE dismissed_doc (
    workspace_id uuid NOT NULL,
    source_id uuid,
    path text NOT NULL,
    dismissed_by text NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE es_events (
    stream_id uuid NOT NULL,
    version bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    metadata jsonb NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT es_events_version_check CHECK ((version > 0))
);

CREATE TABLE es_streams (
    stream_id uuid NOT NULL,
    stream_type text NOT NULL,
    version bigint NOT NULL,
    state jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT es_streams_version_check CHECK ((version > 0))
);

CREATE TABLE finding (
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    check_slug text NOT NULL,
    level text NOT NULL,
    stage text NOT NULL,
    relaxed boolean NOT NULL,
    anchor jsonb NOT NULL,
    message text NOT NULL,
    evidence jsonb DEFAULT '{}'::jsonb NOT NULL,
    suggestion jsonb DEFAULT '{}'::jsonb NOT NULL,
    waived boolean DEFAULT false NOT NULL,
    CONSTRAINT finding_level_check CHECK ((level = ANY (ARRAY['MUST'::text, 'SHOULD'::text, 'INFO'::text])))
);

CREATE TABLE github_connection (
    workspace_id uuid NOT NULL,
    token_encrypted bytea NOT NULL,
    token_last4 text NOT NULL,
    api_url text NOT NULL,
    updated_by text NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE TABLE github_source (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    repo text NOT NULL,
    branch text NOT NULL,
    path text NOT NULL,
    head_commit text DEFAULT ''::text NOT NULL,
    synced_at timestamp with time zone,
    error text DEFAULT ''::text NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    api_url text DEFAULT ''::text NOT NULL,
    skipped jsonb DEFAULT '[]'::jsonb NOT NULL
);

CREATE TABLE handoff (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    version_id uuid NOT NULL,
    verdict text NOT NULL,
    acknowledged boolean NOT NULL,
    label text NOT NULL,
    taken_by text NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE inbox_read (
    user_id text NOT NULL,
    item_key text NOT NULL,
    read_at timestamp with time zone NOT NULL
);

CREATE TABLE invite (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    token_hash text NOT NULL,
    role text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    used_by text,
    revoked_at timestamp with time zone,
    CONSTRAINT invite_role_check CHECK ((role = ANY (ARRAY['admin'::text, 'member'::text])))
);

CREATE TABLE job (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL,
    attempts bigint DEFAULT 0 NOT NULL,
    locked_until timestamp with time zone,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT job_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'done'::text, 'failed'::text])))
);

CREATE TABLE link (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    from_spec_doc_id uuid NOT NULL,
    kind text NOT NULL,
    target_kind text NOT NULL,
    target_spec_doc_id uuid,
    target_ref text NOT NULL,
    origin text NOT NULL,
    target_url text DEFAULT ''::text NOT NULL,
    CONSTRAINT link_kind_check CHECK ((kind = ANY (ARRAY['implements'::text, 'refines'::text, 'references'::text, 'supersedes'::text, 'implemented-by'::text]))),
    CONSTRAINT link_origin_check CHECK ((origin = ANY (ARRAY['frontmatter'::text, 'rule'::text, 'adopted'::text]))),
    CONSTRAINT link_target_kind_check CHECK ((target_kind = ANY (ARRAY['bundle'::text, 'external'::text])))
);

CREATE TABLE link_state (
    spec_doc_id uuid NOT NULL,
    target_ref text NOT NULL,
    state text NOT NULL,
    reason text NOT NULL,
    checked_ref text NOT NULL,
    checked_at timestamp with time zone NOT NULL,
    CONSTRAINT link_state_state_check CHECK ((state = ANY (ARRAY['aligned'::text, 'drifted'::text, 'conflicting'::text, 'unchecked'::text])))
);

CREATE TABLE mcp_connection (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    name text NOT NULL,
    transport text NOT NULL,
    command_or_url jsonb NOT NULL,
    secret_encrypted bytea,
    secret_last4 text NOT NULL,
    tool_allowlist jsonb NOT NULL,
    is_search boolean NOT NULL,
    search_tool text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    hosts jsonb DEFAULT '[]'::jsonb NOT NULL,
    fetch_tool text DEFAULT ''::text NOT NULL,
    CONSTRAINT mcp_connection_transport_check CHECK ((transport = ANY (ARRAY['stdio'::text, 'http'::text])))
);

CREATE TABLE model_backend (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    config jsonb NOT NULL,
    secret_encrypted bytea,
    secret_last4 text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT model_backend_kind_check CHECK ((kind = ANY (ARRAY['openai'::text, 'anthropic'::text, 'openrouter'::text, 'deepseek'::text, 'agent_cli'::text, 'fake'::text])))
);

CREATE TABLE profile (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    key text NOT NULL,
    name text NOT NULL,
    current_version bigint NOT NULL,
    CONSTRAINT profile_current_version_check CHECK ((current_version > 0))
);

CREATE TABLE profile_maintainer (
    profile_id uuid NOT NULL,
    user_id text NOT NULL
);

CREATE TABLE profile_version (
    profile_id uuid NOT NULL,
    version bigint NOT NULL,
    yaml text NOT NULL,
    template text NOT NULL,
    origin text NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT profile_version_version_check CHECK ((version > 0))
);

CREATE TABLE question (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    version_id uuid NOT NULL,
    number bigint NOT NULL,
    text text NOT NULL,
    level text NOT NULL,
    cites jsonb NOT NULL,
    anchor jsonb NOT NULL,
    input_hash text DEFAULT ''::text NOT NULL,
    CONSTRAINT question_level_check CHECK ((level = ANY (ARRAY['MUST'::text, 'SHOULD'::text]))),
    CONSTRAINT question_number_check CHECK ((number > 0))
);

CREATE TABLE question_result (
    run_id uuid NOT NULL,
    question_id uuid NOT NULL,
    result text NOT NULL,
    groups jsonb NOT NULL,
    CONSTRAINT question_result_result_check CHECK ((result = ANY (ARRAY['agree'::text, 'diverge'::text, 'gap'::text])))
);

CREATE TABLE reset_link (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    token_hash text NOT NULL,
    user_id text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone
);

CREATE TABLE review_run (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    version_id uuid NOT NULL,
    profile_key text NOT NULL,
    profile_version bigint NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    stage text NOT NULL,
    roles jsonb DEFAULT '{}'::jsonb NOT NULL,
    prompt_versions jsonb DEFAULT '{}'::jsonb NOT NULL,
    tokens_in bigint DEFAULT 0 NOT NULL,
    tokens_out bigint DEFAULT 0 NOT NULL,
    cost_estimate double precision DEFAULT 0 NOT NULL,
    cache_hits bigint DEFAULT 0 NOT NULL,
    error text DEFAULT ''::text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone,
    notes jsonb DEFAULT '[]'::jsonb NOT NULL,
    stages jsonb DEFAULT '[]'::jsonb NOT NULL,
    decisions_hash text DEFAULT ''::text NOT NULL,
    CONSTRAINT review_run_kind_check CHECK ((kind = ANY (ARRAY['lint'::text, 'full'::text]))),
    CONSTRAINT review_run_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'complete'::text, 'failed'::text])))
);

CREATE TABLE role_assignment (
    workspace_id uuid NOT NULL,
    role text NOT NULL,
    backend_id uuid NOT NULL,
    model text NOT NULL,
    price_in_per_mtok double precision NOT NULL,
    price_out_per_mtok double precision NOT NULL,
    CONSTRAINT role_assignment_role_check CHECK ((role = ANY (ARRAY['reviewer'::text, 'reader_1'::text, 'reader_2'::text, 'reader_3'::text, 'judge'::text, 'writer'::text])))
);

CREATE TABLE run_link (
    run_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    version_id uuid NOT NULL
);

CREATE TABLE share_guest (
    id uuid NOT NULL,
    bundle_id uuid NOT NULL,
    display_name text NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE spec_doc (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    slug text NOT NULL,
    title text NOT NULL,
    profile_key text NOT NULL,
    doc_path text NOT NULL,
    source_kind text NOT NULL,
    source_ref jsonb DEFAULT '{}'::jsonb NOT NULL,
    current_version_id uuid,
    archived_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    bundle_id uuid NOT NULL,
    CONSTRAINT bundle_source_kind_check CHECK ((source_kind = ANY (ARRAY['local'::text, 'db'::text, 'github'::text])))
);

CREATE TABLE spec_doc_reviewer (
    spec_doc_id uuid NOT NULL,
    user_id text NOT NULL
);

CREATE TABLE spec_doc_status_view (
    spec_doc_id uuid NOT NULL,
    status text NOT NULL,
    approvals jsonb NOT NULL,
    approved_version uuid,
    review_requested_at timestamp with time zone,
    approved_at timestamp with time zone,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT bundle_status_view_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'in_review'::text, 'approved'::text, 'superseded'::text])))
);

CREATE TABLE thread_message_view (
    id uuid NOT NULL,
    thread_id uuid NOT NULL,
    seq bigint NOT NULL,
    author_kind text NOT NULL,
    author_id text NOT NULL,
    author_name text NOT NULL,
    body text NOT NULL,
    sources jsonb NOT NULL,
    decision text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT thread_message_view_author_kind_check CHECK ((author_kind = ANY (ARRAY['user'::text, 'guest'::text, 'ai'::text]))),
    CONSTRAINT thread_message_view_decision_check CHECK ((decision = ANY (ARRAY[''::text, 'decision'::text, 'reversal'::text])))
);

CREATE TABLE thread_view (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid,
    profile_key text DEFAULT ''::text NOT NULL,
    anchor_kind text NOT NULL,
    anchor jsonb NOT NULL,
    addressed_to text NOT NULL,
    title text NOT NULL,
    blocking boolean NOT NULL,
    status text NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    last_message_at timestamp with time zone NOT NULL,
    message_count bigint NOT NULL,
    handoff_id uuid,
    handoff_version bigint DEFAULT 0 NOT NULL,
    CONSTRAINT thread_view_addressed_to_check CHECK ((addressed_to = ANY (ARRAY['humans'::text, 'ai'::text]))),
    CONSTRAINT thread_view_anchor_kind_check CHECK ((anchor_kind = ANY (ARRAY['text'::text, 'section'::text, 'finding'::text, 'check'::text]))),
    CONSTRAINT thread_view_status_check CHECK ((status = ANY (ARRAY['open'::text, 'resolved'::text])))
);

CREATE TABLE user_state (
    user_id text NOT NULL,
    inbox_seen_at timestamp with time zone NOT NULL
);

CREATE TABLE verdict (
    run_id uuid NOT NULL,
    result text NOT NULL,
    score bigint NOT NULL,
    radar jsonb NOT NULL,
    waiver_count bigint NOT NULL,
    relaxed_count bigint NOT NULL,
    blocking_finding_ids jsonb NOT NULL,
    items jsonb DEFAULT '[]'::jsonb NOT NULL,
    carried_run_id uuid,
    carried_findings jsonb DEFAULT '[]'::jsonb NOT NULL,
    sections_changed bigint DEFAULT 0 NOT NULL,
    CONSTRAINT verdict_result_check CHECK ((result = ANY (ARRAY['build_ready'::text, 'not_build_ready'::text])))
);

CREATE TABLE verification_outcome (
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    trace_id text NOT NULL,
    outcome text NOT NULL,
    level text NOT NULL,
    blocks boolean NOT NULL,
    waived boolean NOT NULL,
    provenance text NOT NULL,
    note text NOT NULL,
    targets jsonb NOT NULL,
    judgement jsonb NOT NULL
);

CREATE TABLE verification_run (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    version_id uuid NOT NULL,
    handoff_id uuid,
    repo text NOT NULL,
    sha text NOT NULL,
    base_sha text NOT NULL,
    digest text NOT NULL,
    verdict text NOT NULL,
    counts jsonb NOT NULL,
    notes jsonb DEFAULT '[]'::jsonb NOT NULL,
    stale boolean DEFAULT false NOT NULL,
    started_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    status text DEFAULT 'done'::text NOT NULL,
    error text DEFAULT ''::text NOT NULL,
    branch text DEFAULT ''::text NOT NULL
);

CREATE TABLE version (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    number bigint NOT NULL,
    created_by text NOT NULL,
    message text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT version_number_check CHECK ((number > 0))
);

CREATE TABLE version_file (
    version_id uuid NOT NULL,
    path text NOT NULL,
    sha256 text NOT NULL,
    carried_by text DEFAULT ''::text NOT NULL
);

CREATE TABLE waiver_view (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    spec_doc_id uuid NOT NULL,
    check_slug text NOT NULL,
    level text NOT NULL,
    section_path jsonb NOT NULL,
    section_hash text NOT NULL,
    reason text NOT NULL,
    status text NOT NULL,
    requested_by text NOT NULL,
    approvals jsonb NOT NULL,
    decided_by text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    scope text DEFAULT 'check'::text NOT NULL,
    trace_id text DEFAULT ''::text NOT NULL,
    repo text DEFAULT ''::text NOT NULL,
    CONSTRAINT waiver_view_status_check CHECK ((status = ANY (ARRAY['requested'::text, 'approved'::text, 'rejected'::text, 'invalidated'::text])))
);

CREATE TABLE workspace (
    id uuid NOT NULL,
    name text NOT NULL,
    settings jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL
);

ALTER TABLE ONLY adopted_link
    ADD CONSTRAINT adopted_link_pkey PRIMARY KEY (source_id, path, kind);

ALTER TABLE ONLY adopted_type
    ADD CONSTRAINT adopted_type_pkey PRIMARY KEY (source_id, path);

ALTER TABLE ONLY answer
    ADD CONSTRAINT answer_pkey PRIMARY KEY (run_id, question_id, reader_role);

ALTER TABLE ONLY blob
    ADD CONSTRAINT blob_pkey PRIMARY KEY (sha256);

ALTER TABLE ONLY budget
    ADD CONSTRAINT budget_pkey PRIMARY KEY (workspace_id, month);

ALTER TABLE ONLY bundle_author
    ADD CONSTRAINT bundle_author_pkey PRIMARY KEY (bundle_id, user_id);

ALTER TABLE ONLY spec_doc
    ADD CONSTRAINT bundle_pkey PRIMARY KEY (id);

ALTER TABLE ONLY bundle
    ADD CONSTRAINT bundle_pkey1 PRIMARY KEY (id);

ALTER TABLE ONLY spec_doc_reviewer
    ADD CONSTRAINT bundle_reviewer_pkey PRIMARY KEY (spec_doc_id, user_id);

ALTER TABLE ONLY spec_doc_status_view
    ADD CONSTRAINT bundle_status_view_pkey PRIMARY KEY (spec_doc_id);

ALTER TABLE ONLY spec_doc
    ADD CONSTRAINT bundle_workspace_id_slug_key UNIQUE (workspace_id, slug);

ALTER TABLE ONLY bundle
    ADD CONSTRAINT bundle_workspace_id_slug_key1 UNIQUE (workspace_id, slug);

ALTER TABLE ONLY cache_entry
    ADD CONSTRAINT cache_entry_pkey PRIMARY KEY (key_hash);

ALTER TABLE ONLY claim
    ADD CONSTRAINT claim_pkey PRIMARY KEY (id);

ALTER TABLE ONLY content_review
    ADD CONSTRAINT content_review_pkey PRIMARY KEY (id);

ALTER TABLE ONLY es_events
    ADD CONSTRAINT es_events_pkey PRIMARY KEY (stream_id, version);

ALTER TABLE ONLY es_streams
    ADD CONSTRAINT es_streams_pkey PRIMARY KEY (stream_id);

ALTER TABLE ONLY finding
    ADD CONSTRAINT finding_pkey PRIMARY KEY (id);

ALTER TABLE ONLY github_connection
    ADD CONSTRAINT github_connection_pkey PRIMARY KEY (workspace_id);

ALTER TABLE ONLY github_source
    ADD CONSTRAINT github_source_pkey PRIMARY KEY (id);

ALTER TABLE ONLY github_source
    ADD CONSTRAINT github_source_workspace_id_repo_branch_path_key UNIQUE (workspace_id, repo, branch, path);

ALTER TABLE ONLY handoff
    ADD CONSTRAINT handoff_pkey PRIMARY KEY (id);

ALTER TABLE ONLY inbox_read
    ADD CONSTRAINT inbox_read_pkey PRIMARY KEY (user_id, item_key);

ALTER TABLE ONLY invite
    ADD CONSTRAINT invite_pkey PRIMARY KEY (id);

ALTER TABLE ONLY invite
    ADD CONSTRAINT invite_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY job
    ADD CONSTRAINT job_pkey PRIMARY KEY (id);

ALTER TABLE ONLY link
    ADD CONSTRAINT link_pkey PRIMARY KEY (id);

ALTER TABLE ONLY link_state
    ADD CONSTRAINT link_state_pkey PRIMARY KEY (spec_doc_id, target_ref);

ALTER TABLE ONLY mcp_connection
    ADD CONSTRAINT mcp_connection_pkey PRIMARY KEY (id);

ALTER TABLE ONLY mcp_connection
    ADD CONSTRAINT mcp_connection_workspace_id_name_key UNIQUE (workspace_id, name);

ALTER TABLE ONLY model_backend
    ADD CONSTRAINT model_backend_pkey PRIMARY KEY (id);

ALTER TABLE ONLY model_backend
    ADD CONSTRAINT model_backend_workspace_id_name_key UNIQUE (workspace_id, name);

ALTER TABLE ONLY profile_maintainer
    ADD CONSTRAINT profile_maintainer_pkey PRIMARY KEY (profile_id, user_id);

ALTER TABLE ONLY profile
    ADD CONSTRAINT profile_pkey PRIMARY KEY (id);

ALTER TABLE ONLY profile_version
    ADD CONSTRAINT profile_version_pkey PRIMARY KEY (profile_id, version);

ALTER TABLE ONLY profile
    ADD CONSTRAINT profile_workspace_id_key_key UNIQUE (workspace_id, key);

ALTER TABLE ONLY question
    ADD CONSTRAINT question_pkey PRIMARY KEY (id);

ALTER TABLE ONLY question_result
    ADD CONSTRAINT question_result_pkey PRIMARY KEY (run_id, question_id);

ALTER TABLE ONLY question
    ADD CONSTRAINT question_version_id_number_key UNIQUE (version_id, number);

ALTER TABLE ONLY reset_link
    ADD CONSTRAINT reset_link_pkey PRIMARY KEY (id);

ALTER TABLE ONLY reset_link
    ADD CONSTRAINT reset_link_token_hash_key UNIQUE (token_hash);

ALTER TABLE ONLY review_run
    ADD CONSTRAINT review_run_pkey PRIMARY KEY (id);

ALTER TABLE ONLY role_assignment
    ADD CONSTRAINT role_assignment_pkey PRIMARY KEY (workspace_id, role);

ALTER TABLE ONLY run_link
    ADD CONSTRAINT run_link_pkey PRIMARY KEY (run_id, spec_doc_id);

ALTER TABLE ONLY share_guest
    ADD CONSTRAINT share_guest_pkey PRIMARY KEY (id);

ALTER TABLE ONLY thread_message_view
    ADD CONSTRAINT thread_message_view_pkey PRIMARY KEY (id);

ALTER TABLE ONLY thread_message_view
    ADD CONSTRAINT thread_message_view_thread_id_seq_key UNIQUE (thread_id, seq);

ALTER TABLE ONLY thread_view
    ADD CONSTRAINT thread_view_pkey PRIMARY KEY (id);

ALTER TABLE ONLY user_state
    ADD CONSTRAINT user_state_pkey PRIMARY KEY (user_id);

ALTER TABLE ONLY verdict
    ADD CONSTRAINT verdict_pkey PRIMARY KEY (run_id);

ALTER TABLE ONLY verification_outcome
    ADD CONSTRAINT verification_outcome_pkey PRIMARY KEY (id);

ALTER TABLE ONLY verification_run
    ADD CONSTRAINT verification_run_pkey PRIMARY KEY (id);

ALTER TABLE ONLY version
    ADD CONSTRAINT version_bundle_id_number_key UNIQUE (spec_doc_id, number);

ALTER TABLE ONLY version_file
    ADD CONSTRAINT version_file_pkey PRIMARY KEY (version_id, path);

ALTER TABLE ONLY version
    ADD CONSTRAINT version_pkey PRIMARY KEY (id);

ALTER TABLE ONLY waiver_view
    ADD CONSTRAINT waiver_view_pkey PRIMARY KEY (id);

ALTER TABLE ONLY workspace
    ADD CONSTRAINT workspace_pkey PRIMARY KEY (id);

CREATE UNIQUE INDEX bundle_share_token ON bundle USING btree (share_token_hash);

CREATE INDEX claim_run ON claim USING btree (run_id);

CREATE INDEX content_review_created ON content_review USING btree (workspace_id, created_at);

CREATE UNIQUE INDEX dismissed_doc_key ON dismissed_doc USING btree (workspace_id, COALESCE(source_id, '00000000-0000-0000-0000-000000000000'::uuid), path);

CREATE INDEX finding_run ON finding USING btree (run_id);

CREATE INDEX handoff_spec_doc ON handoff USING btree (spec_doc_id, created_at);

CREATE INDEX job_queue ON job USING btree (status, created_at);

CREATE INDEX link_from ON link USING btree (from_spec_doc_id);

CREATE INDEX link_target ON link USING btree (target_spec_doc_id);

CREATE INDEX review_run_spec_doc ON review_run USING btree (spec_doc_id, started_at DESC);

CREATE INDEX spec_doc_bundle ON spec_doc USING btree (bundle_id);

CREATE INDEX thread_view_profile ON thread_view USING btree (workspace_id, profile_key);

CREATE INDEX thread_view_spec_doc ON thread_view USING btree (spec_doc_id, status);

CREATE INDEX verification_outcome_run ON verification_outcome USING btree (run_id, trace_id);

CREATE INDEX verification_run_handoff ON verification_run USING btree (handoff_id);

CREATE INDEX verification_run_spec_doc ON verification_run USING btree (spec_doc_id, created_at);

CREATE INDEX waiver_view_spec_doc ON waiver_view USING btree (spec_doc_id, status);

CREATE INDEX waiver_view_verify ON waiver_view USING btree (spec_doc_id, scope, repo, trace_id);

ALTER TABLE ONLY adopted_link
    ADD CONSTRAINT adopted_link_source_id_fkey FOREIGN KEY (source_id) REFERENCES github_source(id) ON DELETE CASCADE;

ALTER TABLE ONLY adopted_type
    ADD CONSTRAINT adopted_type_source_id_fkey FOREIGN KEY (source_id) REFERENCES github_source(id) ON DELETE CASCADE;

ALTER TABLE ONLY answer
    ADD CONSTRAINT answer_question_id_fkey FOREIGN KEY (question_id) REFERENCES question(id);

ALTER TABLE ONLY answer
    ADD CONSTRAINT answer_run_id_fkey FOREIGN KEY (run_id) REFERENCES review_run(id);

ALTER TABLE ONLY budget
    ADD CONSTRAINT budget_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY bundle_author
    ADD CONSTRAINT bundle_author_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle(id);

ALTER TABLE ONLY spec_doc
    ADD CONSTRAINT bundle_current_version_fk FOREIGN KEY (current_version_id) REFERENCES version(id);

ALTER TABLE ONLY spec_doc_reviewer
    ADD CONSTRAINT bundle_reviewer_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY spec_doc_status_view
    ADD CONSTRAINT bundle_status_view_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY spec_doc
    ADD CONSTRAINT bundle_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY bundle
    ADD CONSTRAINT bundle_workspace_id_fkey1 FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY claim
    ADD CONSTRAINT claim_run_id_fkey FOREIGN KEY (run_id) REFERENCES review_run(id);

ALTER TABLE ONLY content_review
    ADD CONSTRAINT content_review_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY dismissed_doc
    ADD CONSTRAINT dismissed_doc_source_id_fkey FOREIGN KEY (source_id) REFERENCES github_source(id) ON DELETE CASCADE;

ALTER TABLE ONLY dismissed_doc
    ADD CONSTRAINT dismissed_doc_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY es_events
    ADD CONSTRAINT es_events_stream_id_fkey FOREIGN KEY (stream_id) REFERENCES es_streams(stream_id);

ALTER TABLE ONLY finding
    ADD CONSTRAINT finding_run_id_fkey FOREIGN KEY (run_id) REFERENCES review_run(id);

ALTER TABLE ONLY github_connection
    ADD CONSTRAINT github_connection_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY github_source
    ADD CONSTRAINT github_source_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY handoff
    ADD CONSTRAINT handoff_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id) ON DELETE CASCADE;

ALTER TABLE ONLY handoff
    ADD CONSTRAINT handoff_version_id_fkey FOREIGN KEY (version_id) REFERENCES version(id);

ALTER TABLE ONLY handoff
    ADD CONSTRAINT handoff_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY invite
    ADD CONSTRAINT invite_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY job
    ADD CONSTRAINT job_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY link
    ADD CONSTRAINT link_from_bundle_id_fkey FOREIGN KEY (from_spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY link_state
    ADD CONSTRAINT link_state_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id) ON DELETE CASCADE;

ALTER TABLE ONLY link
    ADD CONSTRAINT link_target_bundle_id_fkey FOREIGN KEY (target_spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY link
    ADD CONSTRAINT link_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY mcp_connection
    ADD CONSTRAINT mcp_connection_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY model_backend
    ADD CONSTRAINT model_backend_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY profile_maintainer
    ADD CONSTRAINT profile_maintainer_profile_id_fkey FOREIGN KEY (profile_id) REFERENCES profile(id);

ALTER TABLE ONLY profile_version
    ADD CONSTRAINT profile_version_profile_id_fkey FOREIGN KEY (profile_id) REFERENCES profile(id);

ALTER TABLE ONLY profile
    ADD CONSTRAINT profile_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY question
    ADD CONSTRAINT question_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY question_result
    ADD CONSTRAINT question_result_question_id_fkey FOREIGN KEY (question_id) REFERENCES question(id);

ALTER TABLE ONLY question_result
    ADD CONSTRAINT question_result_run_id_fkey FOREIGN KEY (run_id) REFERENCES review_run(id);

ALTER TABLE ONLY question
    ADD CONSTRAINT question_version_id_fkey FOREIGN KEY (version_id) REFERENCES version(id);

ALTER TABLE ONLY question
    ADD CONSTRAINT question_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY reset_link
    ADD CONSTRAINT reset_link_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY review_run
    ADD CONSTRAINT review_run_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY review_run
    ADD CONSTRAINT review_run_version_id_fkey FOREIGN KEY (version_id) REFERENCES version(id);

ALTER TABLE ONLY review_run
    ADD CONSTRAINT review_run_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY role_assignment
    ADD CONSTRAINT role_assignment_backend_id_fkey FOREIGN KEY (backend_id) REFERENCES model_backend(id);

ALTER TABLE ONLY role_assignment
    ADD CONSTRAINT role_assignment_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY run_link
    ADD CONSTRAINT run_link_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY run_link
    ADD CONSTRAINT run_link_run_id_fkey FOREIGN KEY (run_id) REFERENCES review_run(id);

ALTER TABLE ONLY run_link
    ADD CONSTRAINT run_link_version_id_fkey FOREIGN KEY (version_id) REFERENCES version(id);

ALTER TABLE ONLY share_guest
    ADD CONSTRAINT share_guest_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle(id);

ALTER TABLE ONLY spec_doc
    ADD CONSTRAINT spec_doc_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle(id);

ALTER TABLE ONLY thread_message_view
    ADD CONSTRAINT thread_message_view_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES thread_view(id);

ALTER TABLE ONLY thread_view
    ADD CONSTRAINT thread_view_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY thread_view
    ADD CONSTRAINT thread_view_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY verdict
    ADD CONSTRAINT verdict_run_id_fkey FOREIGN KEY (run_id) REFERENCES review_run(id);

ALTER TABLE ONLY verification_outcome
    ADD CONSTRAINT verification_outcome_run_id_fkey FOREIGN KEY (run_id) REFERENCES verification_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY verification_run
    ADD CONSTRAINT verification_run_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id) ON DELETE CASCADE;

ALTER TABLE ONLY verification_run
    ADD CONSTRAINT verification_run_handoff_id_fkey FOREIGN KEY (handoff_id) REFERENCES handoff(id);

ALTER TABLE ONLY verification_run
    ADD CONSTRAINT verification_run_version_id_fkey FOREIGN KEY (version_id) REFERENCES version(id);

ALTER TABLE ONLY verification_run
    ADD CONSTRAINT verification_run_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY version
    ADD CONSTRAINT version_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY version_file
    ADD CONSTRAINT version_file_sha256_fkey FOREIGN KEY (sha256) REFERENCES blob(sha256);

ALTER TABLE ONLY version_file
    ADD CONSTRAINT version_file_version_id_fkey FOREIGN KEY (version_id) REFERENCES version(id);

ALTER TABLE ONLY version
    ADD CONSTRAINT version_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

ALTER TABLE ONLY waiver_view
    ADD CONSTRAINT waiver_view_bundle_id_fkey FOREIGN KEY (spec_doc_id) REFERENCES spec_doc(id);

ALTER TABLE ONLY waiver_view
    ADD CONSTRAINT waiver_view_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspace(id);

-- +goose Down
DROP TABLE workspace, waiver_view, version_file, version, verification_run, verification_outcome, verdict, user_state, thread_view, thread_message_view, spec_doc_status_view, spec_doc_reviewer, spec_doc, share_guest, run_link, role_assignment, review_run, reset_link, question_result, question, profile_version, profile_maintainer, profile, model_backend, mcp_connection, link_state, link, job, invite, inbox_read, handoff, github_source, github_connection, finding, es_streams, es_events, dismissed_doc, content_review, claim, cache_entry, bundle_author, bundle, budget, blob, answer, adopted_type, adopted_link CASCADE;
