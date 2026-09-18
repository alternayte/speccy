-- +goose Up
-- DEC-020: one workspace per install; every tenant-scoped table has workspace_id.
CREATE TABLE workspace (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    settings   jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL
);

-- DEC-028: content-addressed file contents.
CREATE TABLE blob (
    sha256  text PRIMARY KEY,
    content bytea NOT NULL,
    size    bigint NOT NULL CHECK (size >= 0)
);

CREATE TABLE bundle (
    id                 uuid PRIMARY KEY,
    workspace_id       uuid NOT NULL REFERENCES workspace (id),
    slug               text NOT NULL,
    title              text NOT NULL,
    profile_key        text NOT NULL,
    main_doc           text NOT NULL,
    source_kind        text NOT NULL CHECK (source_kind IN ('local', 'db', 'github')),
    source_ref         jsonb NOT NULL DEFAULT '{}',
    current_version_id uuid,
    archived_at        timestamptz,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    UNIQUE (workspace_id, slug)
);

-- REQ-005: a version is an immutable set of (path, blob hash) pairs.
CREATE TABLE version (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    bundle_id    uuid NOT NULL REFERENCES bundle (id),
    number       bigint NOT NULL CHECK (number > 0),
    created_by   text NOT NULL,
    message      text NOT NULL,
    created_at   timestamptz NOT NULL,
    UNIQUE (bundle_id, number)
);

ALTER TABLE bundle ADD CONSTRAINT bundle_current_version_fk FOREIGN KEY (current_version_id) REFERENCES version (id);

CREATE TABLE version_file (
    version_id uuid NOT NULL REFERENCES version (id),
    path       text NOT NULL,
    sha256     text NOT NULL REFERENCES blob (sha256),
    PRIMARY KEY (version_id, path)
);

-- +goose Down
DROP TABLE version_file;
ALTER TABLE bundle DROP CONSTRAINT bundle_current_version_fk;
DROP TABLE version;
DROP TABLE bundle;
DROP TABLE blob;
DROP TABLE workspace;
