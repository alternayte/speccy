-- +goose Up
-- A bundle is a folder that holds one or more spec docs. Each spec doc keeps its own profile,
-- versions, review runs, waivers, threads, approval and handoffs. Visibility, the share link and
-- the authors belong to the folder. Each existing row becomes a spec doc in a bundle of its own;
-- the next scan groups the spec docs of one folder into one bundle.
ALTER TABLE bundle RENAME TO spec_doc;
ALTER TABLE spec_doc RENAME COLUMN main_doc TO doc_path;
ALTER INDEX bundle_share_token RENAME TO spec_doc_share_token;

CREATE TABLE bundle (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspace (id),
    slug             text NOT NULL,
    title            text NOT NULL,
    source_kind      text NOT NULL CHECK (source_kind IN ('local', 'db', 'github')),
    source_ref       jsonb NOT NULL DEFAULT '{}',
    visibility       text NOT NULL DEFAULT 'internal' CHECK (visibility IN ('private', 'internal', 'link')),
    share_token_hash text,
    share_expires_at timestamptz,
    archived_at      timestamptz,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    UNIQUE (workspace_id, slug)
);
CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);
INSERT INTO bundle (id, workspace_id, slug, title, source_kind, source_ref, visibility, share_token_hash, share_expires_at,
                    archived_at, created_at, updated_at)
SELECT id, workspace_id, slug, title, source_kind, source_ref, visibility, share_token_hash, share_expires_at,
       archived_at, created_at, updated_at
FROM spec_doc;
DROP INDEX spec_doc_share_token;
ALTER TABLE spec_doc DROP COLUMN share_expires_at;
ALTER TABLE spec_doc DROP COLUMN share_token_hash;
ALTER TABLE spec_doc DROP COLUMN visibility;
ALTER TABLE spec_doc ADD COLUMN bundle_id uuid REFERENCES bundle (id);
UPDATE spec_doc SET bundle_id = id;
ALTER TABLE spec_doc ALTER COLUMN bundle_id SET NOT NULL;
CREATE INDEX spec_doc_bundle ON spec_doc (bundle_id);

ALTER TABLE bundle_author DROP CONSTRAINT bundle_author_bundle_id_fkey;
ALTER TABLE bundle_author ADD CONSTRAINT bundle_author_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle (id);
ALTER TABLE share_guest DROP CONSTRAINT share_guest_bundle_id_fkey;
ALTER TABLE share_guest ADD CONSTRAINT share_guest_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle (id);

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
ALTER INDEX review_run_bundle RENAME TO review_run_spec_doc;
ALTER INDEX thread_view_bundle RENAME TO thread_view_spec_doc;
ALTER INDEX waiver_view_bundle RENAME TO waiver_view_spec_doc;
ALTER INDEX handoff_bundle RENAME TO handoff_spec_doc;
ALTER INDEX verification_run_bundle RENAME TO verification_run_spec_doc;

-- +goose Down
ALTER INDEX verification_run_spec_doc RENAME TO verification_run_bundle;
ALTER INDEX handoff_spec_doc RENAME TO handoff_bundle;
ALTER INDEX waiver_view_spec_doc RENAME TO waiver_view_bundle;
ALTER INDEX thread_view_spec_doc RENAME TO thread_view_bundle;
ALTER INDEX review_run_spec_doc RENAME TO review_run_bundle;
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
ALTER TABLE bundle_author DROP CONSTRAINT bundle_author_bundle_id_fkey;
ALTER TABLE share_guest DROP CONSTRAINT share_guest_bundle_id_fkey;
INSERT INTO bundle_author (bundle_id, user_id)
SELECT d.id, a.user_id FROM bundle_author a JOIN spec_doc d ON d.bundle_id = a.bundle_id
ON CONFLICT DO NOTHING;
DELETE FROM bundle_author WHERE bundle_id NOT IN (SELECT id FROM spec_doc);
UPDATE share_guest g SET bundle_id = (SELECT MIN(d.id::text)::uuid FROM spec_doc d WHERE d.bundle_id = g.bundle_id)
WHERE bundle_id NOT IN (SELECT id FROM spec_doc);

ALTER TABLE spec_doc ADD COLUMN visibility text NOT NULL DEFAULT 'internal' CHECK (visibility IN ('private', 'internal', 'link'));
ALTER TABLE spec_doc ADD COLUMN share_token_hash text;
ALTER TABLE spec_doc ADD COLUMN share_expires_at timestamptz;
UPDATE spec_doc d SET visibility = b.visibility FROM bundle b WHERE b.id = d.bundle_id;
UPDATE spec_doc d SET share_token_hash = b.share_token_hash, share_expires_at = b.share_expires_at
FROM bundle b WHERE b.id = d.bundle_id AND d.id = b.id;
DROP INDEX spec_doc_bundle;
ALTER TABLE spec_doc DROP COLUMN bundle_id;
DROP TABLE bundle;
ALTER TABLE spec_doc RENAME COLUMN doc_path TO main_doc;
ALTER TABLE spec_doc RENAME TO bundle;
CREATE UNIQUE INDEX bundle_share_token ON bundle (share_token_hash);
ALTER TABLE bundle_author ADD CONSTRAINT bundle_author_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle (id);
ALTER TABLE share_guest ADD CONSTRAINT share_guest_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES bundle (id);
