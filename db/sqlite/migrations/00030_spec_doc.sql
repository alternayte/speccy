-- +goose Up
-- A bundle is a folder that holds one or more spec docs. Each spec doc keeps its own profile,
-- versions, review runs, waivers, threads, approval and handoffs. Visibility, the share link and
-- the authors belong to the folder. Each existing row becomes a spec doc in a bundle of its own;
-- the next scan groups the spec docs of one folder into one bundle.
-- The rename rewrites the references of the version, run, thread and waiver tables to spec_doc.
ALTER TABLE bundle RENAME TO spec_doc;
DROP INDEX bundle_share_token;

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
CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);
INSERT INTO bundle (id, workspace_id, slug, title, source_kind, source_ref, visibility, share_token_hash, share_expires_at,
                    archived_at, created_at, updated_at)
SELECT id, workspace_id, slug, title, source_kind, source_ref, visibility, share_token_hash, share_expires_at,
       archived_at, created_at, updated_at
FROM spec_doc;

-- SQLite cannot add a NOT NULL reference, so spec_doc is rebuilt under a new name and renamed
-- back. The references of the other tables name spec_doc and hold until the commit.
PRAGMA defer_foreign_keys = ON;
CREATE TABLE spec_doc_new (
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
INSERT INTO spec_doc_new (id, workspace_id, slug, title, profile_key, doc_path, source_kind, source_ref,
                          current_version_id, archived_at, created_at, updated_at, bundle_id)
SELECT id, workspace_id, slug, title, profile_key, main_doc, source_kind, source_ref,
       current_version_id, archived_at, created_at, updated_at, id
FROM spec_doc;
DROP TABLE spec_doc;
ALTER TABLE spec_doc_new RENAME TO spec_doc;
CREATE INDEX spec_doc_bundle ON spec_doc (bundle_id);

ALTER TABLE bundle_author RENAME TO bundle_author_old;
CREATE TABLE bundle_author (
    bundle_id UUIDTEXT NOT NULL REFERENCES bundle (id),
    user_id   TEXT NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);
INSERT INTO bundle_author (bundle_id, user_id) SELECT bundle_id, user_id FROM bundle_author_old;
DROP TABLE bundle_author_old;
ALTER TABLE share_guest RENAME TO share_guest_old;
CREATE TABLE share_guest (
    id           UUIDTEXT PRIMARY KEY,
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id),
    display_name TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);
INSERT INTO share_guest (id, bundle_id, display_name, created_at) SELECT id, bundle_id, display_name, created_at FROM share_guest_old;
DROP TABLE share_guest_old;

ALTER TABLE version RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE review_run RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE question RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE link RENAME COLUMN from_bundle_id TO from_spec_doc_id;
ALTER TABLE link RENAME COLUMN target_bundle_id TO target_spec_doc_id;
ALTER TABLE run_link RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE bundle_reviewer RENAME TO spec_doc_reviewer;
ALTER TABLE spec_doc_reviewer RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE thread_view RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE waiver_view RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE bundle_status_view RENAME TO spec_doc_status_view;
ALTER TABLE spec_doc_status_view RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE handoff RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE link_state RENAME COLUMN bundle_id TO spec_doc_id;
ALTER TABLE verification_run RENAME COLUMN bundle_id TO spec_doc_id;
DROP INDEX review_run_bundle;
CREATE INDEX review_run_spec_doc ON review_run (spec_doc_id, started_at DESC);
DROP INDEX thread_view_bundle;
CREATE INDEX thread_view_spec_doc ON thread_view (spec_doc_id, status);
DROP INDEX waiver_view_bundle;
CREATE INDEX waiver_view_spec_doc ON waiver_view (spec_doc_id, status);
DROP INDEX handoff_bundle;
CREATE INDEX handoff_spec_doc ON handoff (spec_doc_id, created_at);
DROP INDEX verification_run_bundle;
CREATE INDEX verification_run_spec_doc ON verification_run (spec_doc_id, created_at);

-- +goose Down
DROP INDEX verification_run_spec_doc;
CREATE INDEX verification_run_bundle ON verification_run (spec_doc_id, created_at);
DROP INDEX handoff_spec_doc;
CREATE INDEX handoff_bundle ON handoff (spec_doc_id, created_at);
DROP INDEX waiver_view_spec_doc;
CREATE INDEX waiver_view_bundle ON waiver_view (spec_doc_id, status);
DROP INDEX thread_view_spec_doc;
CREATE INDEX thread_view_bundle ON thread_view (spec_doc_id, status);
DROP INDEX review_run_spec_doc;
CREATE INDEX review_run_bundle ON review_run (spec_doc_id, started_at DESC);
ALTER TABLE verification_run RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE link_state RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE handoff RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE spec_doc_status_view RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE spec_doc_status_view RENAME TO bundle_status_view;
ALTER TABLE waiver_view RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE thread_view RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE spec_doc_reviewer RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE spec_doc_reviewer RENAME TO bundle_reviewer;
ALTER TABLE run_link RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE link RENAME COLUMN target_spec_doc_id TO target_bundle_id;
ALTER TABLE link RENAME COLUMN from_spec_doc_id TO from_bundle_id;
ALTER TABLE question RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE review_run RENAME COLUMN spec_doc_id TO bundle_id;
ALTER TABLE version RENAME COLUMN spec_doc_id TO bundle_id;

-- An author or a guest of a folder becomes an author or a guest of each spec doc in it.
CREATE TABLE bundle_author_old AS SELECT d.id AS bundle_id, a.user_id FROM bundle_author a JOIN spec_doc d ON d.bundle_id = a.bundle_id;
DROP TABLE bundle_author;
CREATE TABLE share_guest_old AS
SELECT g.id, (SELECT MIN(d.id) FROM spec_doc d WHERE d.bundle_id = g.bundle_id) AS bundle_id, g.display_name, g.created_at
FROM share_guest g;
DROP TABLE share_guest;

PRAGMA defer_foreign_keys = ON;
CREATE TABLE spec_doc_new (
    id                 UUIDTEXT PRIMARY KEY,
    workspace_id       UUIDTEXT NOT NULL REFERENCES workspace (id),
    slug               TEXT NOT NULL,
    title              TEXT NOT NULL,
    profile_key        TEXT NOT NULL,
    main_doc           TEXT NOT NULL,
    source_kind        TEXT NOT NULL CHECK (source_kind IN ('local', 'db', 'github')),
    source_ref         JSONTEXT NOT NULL DEFAULT '{}' CHECK (json_valid(source_ref)),
    current_version_id UUIDTEXT REFERENCES version (id),
    archived_at        DATETIME,
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    visibility         TEXT NOT NULL DEFAULT 'internal' CHECK (visibility IN ('private', 'internal', 'link')),
    share_token_hash   TEXT,
    share_expires_at   DATETIME,
    UNIQUE (workspace_id, slug)
);
INSERT INTO spec_doc_new (id, workspace_id, slug, title, profile_key, main_doc, source_kind, source_ref, current_version_id,
                          archived_at, created_at, updated_at, visibility, share_token_hash, share_expires_at)
SELECT d.id, d.workspace_id, d.slug, d.title, d.profile_key, d.doc_path, d.source_kind, d.source_ref, d.current_version_id,
       d.archived_at, d.created_at, d.updated_at, b.visibility,
       CASE WHEN b.id = d.id THEN b.share_token_hash END, CASE WHEN b.id = d.id THEN b.share_expires_at END
FROM spec_doc d JOIN bundle b ON b.id = d.bundle_id;
DROP TABLE spec_doc;
DROP TABLE bundle;
ALTER TABLE spec_doc_new RENAME TO spec_doc;
ALTER TABLE spec_doc RENAME TO bundle;
CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);

CREATE TABLE bundle_author (
    bundle_id UUIDTEXT NOT NULL REFERENCES bundle (id),
    user_id   TEXT NOT NULL,
    PRIMARY KEY (bundle_id, user_id)
);
INSERT OR IGNORE INTO bundle_author (bundle_id, user_id) SELECT bundle_id, user_id FROM bundle_author_old;
DROP TABLE bundle_author_old;
CREATE TABLE share_guest (
    id           UUIDTEXT PRIMARY KEY,
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id),
    display_name TEXT NOT NULL,
    created_at   DATETIME NOT NULL
);
INSERT INTO share_guest (id, bundle_id, display_name, created_at) SELECT id, bundle_id, display_name, created_at FROM share_guest_old;
DROP TABLE share_guest_old;
