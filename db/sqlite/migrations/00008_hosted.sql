-- +goose Up
-- REQ-081, REQ-083: single-use invite links with a role. The token is stored hashed (§14.2).
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

-- REQ-082: one-time password reset links, made by an admin.
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

-- REQ-084, REQ-085: visibility and the share link of a bundle.
ALTER TABLE bundle ADD COLUMN visibility TEXT NOT NULL DEFAULT 'internal' CHECK (visibility IN ('private', 'internal', 'link'));
ALTER TABLE bundle ADD COLUMN share_token_hash TEXT;
ALTER TABLE bundle ADD COLUMN share_expires_at DATETIME;
CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);

-- SDD §11.1: the authors and the named members (reviewers) of a bundle. User IDs are
-- auth-all user IDs.
CREATE TABLE bundle_author (
    bundle_id UUIDTEXT NOT NULL REFERENCES bundle (id),
    user_id   TEXT NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);
CREATE TABLE bundle_reviewer (
    bundle_id UUIDTEXT NOT NULL REFERENCES bundle (id),
    user_id   TEXT NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);

-- REQ-086: a guest on a share link, with a display name.
CREATE TABLE share_guest (
    id           UUIDTEXT PRIMARY KEY,
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id),
    display_name TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);

-- +goose Down
DROP TABLE share_guest;
DROP TABLE bundle_reviewer;
DROP TABLE bundle_author;
DROP INDEX bundle_share_token;
ALTER TABLE bundle DROP COLUMN share_expires_at;
ALTER TABLE bundle DROP COLUMN share_token_hash;
ALTER TABLE bundle DROP COLUMN visibility;
DROP TABLE reset_link;
DROP TABLE invite;
