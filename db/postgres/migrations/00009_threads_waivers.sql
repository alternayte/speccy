-- +goose Up
-- DEC-008, §11.3: inline projections of the thread, waiver, and bundle_status streams.
CREATE TABLE thread_view (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspace (id),
    bundle_id       uuid REFERENCES bundle (id),
    profile_key     text NOT NULL DEFAULT '',
    anchor_kind     text NOT NULL CHECK (anchor_kind IN ('text', 'section', 'finding', 'check')),
    anchor          jsonb NOT NULL,
    addressed_to    text NOT NULL CHECK (addressed_to IN ('humans', 'ai')),
    title           text NOT NULL,
    blocking        boolean NOT NULL,
    status          text NOT NULL CHECK (status IN ('open', 'resolved')),
    created_by      text NOT NULL,
    created_at      timestamptz NOT NULL,
    last_message_at timestamptz NOT NULL,
    message_count   bigint NOT NULL
);
CREATE INDEX thread_view_bundle ON thread_view (bundle_id, status);
CREATE INDEX thread_view_profile ON thread_view (workspace_id, profile_key);

CREATE TABLE thread_message_view (
    id          uuid PRIMARY KEY,
    thread_id   uuid NOT NULL REFERENCES thread_view (id),
    seq         bigint NOT NULL,
    author_kind text NOT NULL CHECK (author_kind IN ('user', 'guest', 'ai')),
    author_id   text NOT NULL,
    author_name text NOT NULL,
    body        text NOT NULL,
    sources     jsonb NOT NULL,
    decision    text NOT NULL CHECK (decision IN ('', 'decision', 'reversal')),
    created_at  timestamptz NOT NULL,
    UNIQUE (thread_id, seq)
);

CREATE TABLE waiver_view (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspace (id),
    bundle_id     uuid NOT NULL REFERENCES bundle (id),
    check_slug    text NOT NULL,
    level         text NOT NULL,
    section_path  jsonb NOT NULL,
    section_hash  text NOT NULL,
    reason        text NOT NULL,
    status        text NOT NULL CHECK (status IN ('requested', 'approved', 'rejected', 'invalidated')),
    requested_by  text NOT NULL,
    approvals     jsonb NOT NULL,
    decided_by    text NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);
CREATE INDEX waiver_view_bundle ON waiver_view (bundle_id, status);

CREATE TABLE bundle_status_view (
    bundle_id        uuid PRIMARY KEY REFERENCES bundle (id),
    status           text NOT NULL CHECK (status IN ('draft', 'in_review', 'approved', 'superseded')),
    approvals        jsonb NOT NULL,
    approved_version uuid,
    review_requested_at timestamptz,
    approved_at      timestamptz,
    updated_at       timestamptz NOT NULL
);

-- SDD §3: the maintainers of a profile.
CREATE TABLE profile_maintainer (
    profile_id uuid NOT NULL REFERENCES profile (id),
    user_id    text NOT NULL,
    PRIMARY KEY (profile_id, user_id)
);

-- REQ-091: when each user last read the inbox.
CREATE TABLE user_state (
    user_id       text PRIMARY KEY,
    inbox_seen_at timestamptz NOT NULL
);

-- REQ-074: a finding that a valid waiver covers.
ALTER TABLE finding ADD COLUMN waived boolean NOT NULL DEFAULT false;

-- REQ-047: the content hash the questions were made for, so a version with the same content
-- reuses them.
ALTER TABLE question ADD COLUMN input_hash text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE question DROP COLUMN input_hash;
ALTER TABLE finding DROP COLUMN waived;
DROP TABLE user_state;
DROP TABLE profile_maintainer;
DROP TABLE bundle_status_view;
DROP TABLE waiver_view;
DROP TABLE thread_message_view;
DROP TABLE thread_view;
