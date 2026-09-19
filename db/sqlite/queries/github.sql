-- name: GetGithubConnection :one
SELECT * FROM github_connection WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertGithubConnection :exec
INSERT INTO github_connection (workspace_id, token_encrypted, token_last4, api_url, updated_by, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(token_encrypted), sqlc.arg(token_last4), sqlc.arg(api_url), sqlc.arg(updated_by), sqlc.arg(updated_at))
ON CONFLICT (workspace_id) DO UPDATE SET token_encrypted = excluded.token_encrypted, token_last4 = excluded.token_last4,
    api_url = excluded.api_url, updated_by = excluded.updated_by, updated_at = excluded.updated_at;

-- name: DeleteGithubConnection :exec
DELETE FROM github_connection WHERE workspace_id = sqlc.arg(workspace_id);

-- name: ListGithubSources :many
SELECT * FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY repo, branch, path;

-- name: GetGithubSource :one
SELECT * FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: InsertGithubSource :exec
INSERT INTO github_source (id, workspace_id, repo, branch, path, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(repo), sqlc.arg(branch), sqlc.arg(path), sqlc.arg(created_by), sqlc.arg(created_at));

-- name: DeleteGithubSource :exec
DELETE FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetGithubSourceSynced :exec
UPDATE github_source SET head_commit = sqlc.arg(head_commit), synced_at = sqlc.arg(synced_at), error = sqlc.arg(error)
WHERE id = sqlc.arg(id);

-- name: SetBundleSourceRef :exec
UPDATE bundle SET source_ref = sqlc.arg(source_ref), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);
