-- +goose Up
-- DEC-020: one workspace per install; every tenant-scoped table has workspace_id.
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

CREATE TABLE bundle (
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
    UNIQUE (workspace_id, slug)
);

CREATE TABLE version (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id),
    number       INTEGER NOT NULL CHECK (number > 0),
    created_by   TEXT NOT NULL,
    message      TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    UNIQUE (bundle_id, number)
);

CREATE TABLE version_file (
    version_id UUIDTEXT NOT NULL REFERENCES version (id),
    path       TEXT NOT NULL,
    sha256     TEXT NOT NULL REFERENCES blob (sha256),
    PRIMARY KEY (version_id, path)
);

-- +goose Down
DROP TABLE version_file;
DROP TABLE version;
DROP TABLE bundle;
DROP TABLE blob;
DROP TABLE workspace;
