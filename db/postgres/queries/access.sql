-- name: InsertInvite :exec
INSERT INTO invite (id, workspace_id, token_hash, role, expires_at, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(token_hash), sqlc.arg(role), sqlc.arg(expires_at),
        sqlc.arg(created_by), sqlc.arg(created_at));

-- name: ListInvites :many
SELECT * FROM invite WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY created_at DESC LIMIT 200;

-- name: RevokeInvite :execrows
UPDATE invite SET revoked_at = sqlc.arg(revoked_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND used_at IS NULL AND revoked_at IS NULL;

-- name: PeekInvite :one
SELECT * FROM invite
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND revoked_at IS NULL AND expires_at > sqlc.arg(now);

-- name: SpendInvite :one
-- One conditional update spends an invite, so two parallel acceptances use it once.
UPDATE invite SET used_at = sqlc.arg(used_at), used_by = sqlc.arg(used_by)
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND revoked_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: UnspendInvite :exec
UPDATE invite SET used_at = NULL, used_by = NULL WHERE id = sqlc.arg(id);

-- name: InsertResetLink :exec
INSERT INTO reset_link (id, workspace_id, token_hash, user_id, expires_at, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(expires_at),
        sqlc.arg(created_by), sqlc.arg(created_at));

-- name: PeekResetLink :one
SELECT * FROM reset_link WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND expires_at > sqlc.arg(now);

-- name: SpendResetLink :one
UPDATE reset_link SET used_at = sqlc.arg(used_at)
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: InsertBundleAuthor :exec
INSERT INTO bundle_author (bundle_id, user_id) VALUES (sqlc.arg(bundle_id), sqlc.arg(user_id)) ON CONFLICT DO NOTHING;

-- name: ListBundleAuthors :many
SELECT user_id FROM bundle_author WHERE bundle_id = sqlc.arg(bundle_id) ORDER BY user_id;

-- name: ListBundleReviewers :many
SELECT user_id FROM bundle_reviewer WHERE bundle_id = sqlc.arg(bundle_id) ORDER BY user_id;

-- name: IsBundleMember :one
-- An author or a named member (reviewer) of the bundle.
SELECT EXISTS (
    SELECT 1 FROM bundle_author WHERE bundle_author.bundle_id = sqlc.arg(bundle_id) AND bundle_author.user_id = sqlc.arg(user_id)
    UNION ALL
    SELECT 1 FROM bundle_reviewer WHERE bundle_reviewer.bundle_id = sqlc.arg(bundle_id) AND bundle_reviewer.user_id = sqlc.arg(user_id)
) AS member;

-- name: IsBundleAuthor :one
SELECT EXISTS (SELECT 1 FROM bundle_author WHERE bundle_id = sqlc.arg(bundle_id) AND user_id = sqlc.arg(user_id)) AS author;

-- name: SetBundleVisibility :exec
UPDATE bundle SET visibility = sqlc.arg(visibility), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: SetBundleShare :exec
UPDATE bundle SET share_token_hash = sqlc.narg(share_token_hash), share_expires_at = sqlc.narg(share_expires_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: BundleByShareToken :one
SELECT * FROM bundle
WHERE share_token_hash = sqlc.arg(share_token_hash) AND visibility = 'link' AND archived_at IS NULL
  AND (share_expires_at IS NULL OR share_expires_at > sqlc.arg(now));

-- name: InsertShareGuest :exec
INSERT INTO share_guest (id, bundle_id, display_name, created_at)
VALUES (sqlc.arg(id), sqlc.arg(bundle_id), sqlc.arg(display_name), sqlc.arg(created_at));

-- name: GetShareGuest :one
SELECT * FROM share_guest WHERE id = sqlc.arg(id);

-- name: SetWorkspaceSettings :exec
UPDATE workspace SET settings = sqlc.arg(settings) WHERE id = sqlc.arg(id);

-- name: GetWorkspace :one
SELECT * FROM workspace WHERE id = sqlc.arg(id);
