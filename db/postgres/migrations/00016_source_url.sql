-- +goose Up
-- REQ-123, REQ-128: a source's path is a folder or one doc, and a source URL may name a host
-- other than github.com. profile is the profile of a one-doc source whose doc has no type and
-- no mapping in the repo, so Speccy writes nothing into the repo.
ALTER TABLE github_source ADD COLUMN is_file BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE github_source ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE github_source ADD COLUMN api_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE github_source DROP COLUMN is_file;
ALTER TABLE github_source DROP COLUMN profile;
ALTER TABLE github_source DROP COLUMN api_url;
