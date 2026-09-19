-- +goose Up
-- REQ-081, REQ-083: single-use invite links with a role. The token is stored hashed (§14.2).
CREATE TABLE invite (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    token_hash   text NOT NULL UNIQUE,
    role         text NOT NULL CHECK (role IN ('admin', 'member')),
    expires_at   timestamptz NOT NULL,
    created_by   text NOT NULL,
    created_at   timestamptz NOT NULL,
    used_at      timestamptz,
    used_by      text,
    revoked_at   timestamptz
);

-- REQ-082: one-time password reset links, made by an admin.
CREATE TABLE reset_link (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    token_hash   text NOT NULL UNIQUE,
    user_id      text NOT NULL,
    expires_at   timestamptz NOT NULL,
    created_by   text NOT NULL,
    created_at   timestamptz NOT NULL,
    used_at      timestamptz
);

-- REQ-084, REQ-085: visibility and the share link of a bundle.
ALTER TABLE bundle ADD COLUMN visibility text NOT NULL DEFAULT 'internal' CHECK (visibility IN ('private', 'internal', 'link'));
ALTER TABLE bundle ADD COLUMN share_token_hash text;
ALTER TABLE bundle ADD COLUMN share_expires_at timestamptz;
CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);

-- SDD §11.1: the authors and the named members (reviewers) of a bundle. User IDs are
-- auth-all user IDs.
CREATE TABLE bundle_author (
    bundle_id uuid NOT NULL REFERENCES bundle (id),
    user_id   text NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);
CREATE TABLE bundle_reviewer (
    bundle_id uuid NOT NULL REFERENCES bundle (id),
    user_id   text NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);

-- REQ-086: a guest on a share link, with a display name.
CREATE TABLE share_guest (
    id           uuid PRIMARY KEY,
    bundle_id    uuid NOT NULL REFERENCES bundle (id),
    display_name text NOT NULL,
    created_at   timestamptz NOT NULL
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
