-- name: GetFirstWorkspace :one
SELECT id, name, settings, created_at FROM workspace ORDER BY created_at, id LIMIT 1;

-- name: InsertWorkspace :exec
INSERT INTO workspace (id, name, settings, created_at) VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(settings), sqlc.arg(created_at));

-- name: InsertBlob :exec
INSERT INTO blob (sha256, content, size) VALUES (sqlc.arg(sha256), sqlc.arg(content), sqlc.arg(size))
ON CONFLICT (sha256) DO NOTHING;

-- name: GetBlob :one
SELECT content FROM blob WHERE sha256 = sqlc.arg(sha256);

-- name: InsertBundle :exec
INSERT INTO bundle (id, workspace_id, slug, title, profile_key, main_doc, source_kind, source_ref, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(slug), sqlc.arg(title), sqlc.arg(profile_key), sqlc.arg(main_doc),
        sqlc.arg(source_kind), sqlc.arg(source_ref), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetBundle :one
SELECT * FROM bundle WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetBundleBySlug :one
SELECT * FROM bundle WHERE workspace_id = sqlc.arg(workspace_id) AND slug = sqlc.arg(slug);

-- name: ListBundles :many
SELECT * FROM bundle
WHERE workspace_id = sqlc.arg(workspace_id) AND archived_at IS NULL AND slug > sqlc.arg(after_slug)
ORDER BY slug
LIMIT sqlc.arg(page_size);

-- name: ListBundlesBySource :many
SELECT * FROM bundle
WHERE workspace_id = sqlc.arg(workspace_id) AND source_kind = sqlc.arg(source_kind)
ORDER BY slug;

-- name: UpdateBundleHead :execrows
-- The head moves only from the version the change was based on.
UPDATE bundle
SET title = sqlc.arg(title), profile_key = sqlc.arg(profile_key), main_doc = sqlc.arg(main_doc), current_version_id = sqlc.arg(current_version_id),
    archived_at = NULL, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND current_version_id IS sqlc.narg(expected_version_id);

-- name: SetBundleArchived :exec
UPDATE bundle SET archived_at = sqlc.narg(archived_at), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: InsertVersion :exec
INSERT INTO version (id, workspace_id, bundle_id, number, created_by, message, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(bundle_id), sqlc.arg(number), sqlc.arg(created_by),
        sqlc.arg(message), sqlc.arg(created_at));

-- name: InsertVersionFile :exec
INSERT INTO version_file (version_id, path, sha256) VALUES (sqlc.arg(version_id), sqlc.arg(path), sqlc.arg(sha256));

-- name: NextVersionNumber :one
SELECT CAST(COALESCE(MAX(number), 0) + 1 AS BIGINT) AS next FROM version WHERE bundle_id = sqlc.arg(bundle_id);

-- name: GetVersion :one
SELECT * FROM version WHERE bundle_id = sqlc.arg(bundle_id) AND id = sqlc.arg(id);

-- name: GetVersionByNumber :one
SELECT * FROM version WHERE bundle_id = sqlc.arg(bundle_id) AND number = sqlc.arg(number);

-- name: ListVersions :many
SELECT * FROM version
WHERE bundle_id = sqlc.arg(bundle_id) AND number < sqlc.arg(before_number)
ORDER BY number DESC
LIMIT sqlc.arg(page_size);

-- name: ListVersionFiles :many
SELECT vf.path, vf.sha256, b.size
FROM version_file vf JOIN blob b ON b.sha256 = vf.sha256
WHERE vf.version_id = sqlc.arg(version_id)
ORDER BY vf.path;
