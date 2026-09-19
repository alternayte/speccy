-- +goose Up
-- DEC-008, §11.3: inline projections of the thread, waiver, and bundle_status streams.
CREATE TABLE thread_view (
    id              UUIDTEXT PRIMARY KEY,
    workspace_id    UUIDTEXT NOT NULL REFERENCES workspace (id),
    bundle_id       UUIDTEXT REFERENCES bundle (id),
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
);
CREATE INDEX thread_view_bundle ON thread_view (bundle_id, status);
CREATE INDEX thread_view_profile ON thread_view (workspace_id, profile_key);

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
    bundle_id     UUIDTEXT NOT NULL REFERENCES bundle (id),
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
);
CREATE INDEX waiver_view_bundle ON waiver_view (bundle_id, status);

CREATE TABLE bundle_status_view (
    bundle_id        UUIDTEXT PRIMARY KEY REFERENCES bundle (id),
    status           TEXT NOT NULL CHECK (status IN ('draft', 'in_review', 'approved', 'superseded')),
    approvals        JSONTEXT NOT NULL,
    approved_version UUIDTEXT,
    review_requested_at DATETIME,
    approved_at      DATETIME,
    updated_at       DATETIME NOT NULL
);

-- SDD §3: the maintainers of a profile.
CREATE TABLE profile_maintainer (
    profile_id UUIDTEXT NOT NULL REFERENCES profile (id),
    user_id    TEXT NOT NULL,
    PRIMARY KEY (profile_id, user_id)
);

-- REQ-091: when each user last read the inbox.
CREATE TABLE user_state (
    user_id       TEXT PRIMARY KEY,
    inbox_seen_at DATETIME NOT NULL
);

-- REQ-074: a finding that a valid waiver covers.
ALTER TABLE finding ADD COLUMN waived BOOLEAN NOT NULL DEFAULT false;

-- REQ-047: the content hash the questions were made for, so a version with the same content
-- reuses them.
ALTER TABLE question ADD COLUMN input_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE question DROP COLUMN input_hash;
ALTER TABLE finding DROP COLUMN waived;
DROP TABLE user_state;
DROP TABLE profile_maintainer;
DROP TABLE bundle_status_view;
DROP TABLE waiver_view;
DROP TABLE thread_message_view;
DROP TABLE thread_view;
