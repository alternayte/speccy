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
INSERT INTO github_source (id, workspace_id, repo, branch, path, api_url, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(repo), sqlc.arg(branch), sqlc.arg(path),
        sqlc.arg(api_url), sqlc.arg(created_by), sqlc.arg(created_at));

-- name: DeleteGithubSource :exec
DELETE FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetGithubSourceSynced :exec
UPDATE github_source SET head_commit = sqlc.arg(head_commit), synced_at = sqlc.arg(synced_at), error = sqlc.arg(error)
WHERE id = sqlc.arg(id);

-- name: SetSpecDocSourceRef :exec
UPDATE spec_doc SET source_ref = sqlc.arg(source_ref), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: ListAdoptedTypes :many
SELECT * FROM adopted_type WHERE source_id = sqlc.arg(source_id) ORDER BY path;

-- name: SetAdoptedType :exec
INSERT INTO adopted_type (source_id, path, profile) VALUES (sqlc.arg(source_id), sqlc.arg(path), sqlc.arg(profile))
ON CONFLICT (source_id, path) DO UPDATE SET profile = excluded.profile;

-- name: DeleteAdoptedType :exec
DELETE FROM adopted_type WHERE source_id = sqlc.arg(source_id) AND path = sqlc.arg(path);

-- name: SetGithubSourceSkipped :exec
UPDATE github_source SET skipped = sqlc.arg(skipped) WHERE id = sqlc.arg(id);

-- name: SetGithubSourceRepoConfig :exec
UPDATE github_source SET repo_config = sqlc.arg(repo_config) WHERE id = sqlc.arg(id);

-- name: ListDismissedDocs :many
SELECT * FROM dismissed_doc WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY path;

-- name: InsertDismissedDoc :exec
INSERT INTO dismissed_doc (workspace_id, source_id, path, dismissed_by, created_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(source_id), sqlc.arg(path), sqlc.arg(dismissed_by), sqlc.arg(created_at))
ON CONFLICT DO NOTHING;

-- name: DeleteDismissedDoc :exec
DELETE FROM dismissed_doc WHERE workspace_id = sqlc.arg(workspace_id)
  AND coalesce(source_id, '00000000-0000-0000-0000-000000000000') = coalesce(sqlc.narg(source_id), '00000000-0000-0000-0000-000000000000')
  AND path = sqlc.arg(path);

-- name: ListAdoptedLinks :many
SELECT * FROM adopted_link WHERE source_id = sqlc.arg(source_id) ORDER BY path, kind;

-- name: SetAdoptedLink :exec
INSERT INTO adopted_link (source_id, path, kind, target)
VALUES (sqlc.arg(source_id), sqlc.arg(path), sqlc.arg(kind), sqlc.arg(target))
ON CONFLICT (source_id, path, kind) DO UPDATE SET target = excluded.target;

-- name: DeleteAdoptedLink :exec
DELETE FROM adopted_link WHERE source_id = sqlc.arg(source_id) AND path = sqlc.arg(path) AND kind = sqlc.arg(kind);

-- name: MoveAdoptedTypes :exec
-- A source that covers another takes over its adopted types.
UPDATE adopted_type SET source_id = sqlc.arg(to_source) WHERE source_id = sqlc.arg(from_source);

-- name: MoveAdoptedLinks :exec
UPDATE adopted_link SET source_id = sqlc.arg(to_source) WHERE source_id = sqlc.arg(from_source);

-- name: GetGithubApp :one
SELECT * FROM github_app WHERE workspace_id = sqlc.arg(workspace_id);

-- name: InsertGithubApp :exec
INSERT INTO github_app (workspace_id, api_url, app_id, slug, owner_login, owner_is_org, client_id, client_secret_encrypted,
                        private_key_encrypted, webhook_secret_encrypted, created_by, created_at, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(api_url), sqlc.arg(app_id), sqlc.arg(slug), sqlc.arg(owner_login), sqlc.arg(owner_is_org),
        sqlc.arg(client_id), sqlc.arg(client_secret_encrypted), sqlc.arg(private_key_encrypted), sqlc.arg(webhook_secret_encrypted),
        sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: SetGithubAppInstallation :exec
UPDATE github_app SET installation_id = sqlc.narg(installation_id), updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: DeleteGithubApp :exec
DELETE FROM github_app WHERE workspace_id = sqlc.arg(workspace_id);

-- name: GetGithubUserToken :one
SELECT * FROM github_user_token WHERE workspace_id = sqlc.arg(workspace_id) AND github_user_id = sqlc.arg(github_user_id);

-- name: UpsertGithubUserToken :exec
INSERT INTO github_user_token (workspace_id, github_user_id, login, access_encrypted, access_expires_at, refresh_encrypted,
                               refresh_expires_at, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(github_user_id), sqlc.arg(login), sqlc.arg(access_encrypted), sqlc.narg(access_expires_at),
        sqlc.narg(refresh_encrypted), sqlc.narg(refresh_expires_at), sqlc.arg(updated_at))
ON CONFLICT (workspace_id, github_user_id) DO UPDATE SET login = excluded.login, access_encrypted = excluded.access_encrypted,
    access_expires_at = excluded.access_expires_at, refresh_encrypted = excluded.refresh_encrypted,
    refresh_expires_at = excluded.refresh_expires_at, updated_at = excluded.updated_at;

-- name: DeleteGithubUserToken :exec
DELETE FROM github_user_token WHERE workspace_id = sqlc.arg(workspace_id) AND github_user_id = sqlc.arg(github_user_id);

-- name: DeleteGithubUserTokens :exec
DELETE FROM github_user_token WHERE workspace_id = sqlc.arg(workspace_id);

-- name: ListGithubSourcesOfRepo :many
SELECT * FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) AND lower(repo) = lower(sqlc.arg(repo)) ORDER BY branch, path;

-- name: SetGithubSourcePull :exec
UPDATE github_source SET pull = sqlc.arg(pull), pull_title = sqlc.arg(pull_title), pull_author = sqlc.arg(pull_author),
    pull_author_id = sqlc.arg(pull_author_id), pull_state = sqlc.arg(pull_state), base_branch = sqlc.arg(base_branch),
    branch = sqlc.arg(branch), docs = sqlc.arg(docs)
WHERE id = sqlc.arg(id);

-- name: ListGithubSourcesOfPull :many
SELECT * FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) AND lower(repo) = lower(sqlc.arg(repo)) AND pull = sqlc.arg(pull)
ORDER BY created_at;

-- name: ListGithubSourcesOfAuthor :many
SELECT * FROM github_source WHERE workspace_id = sqlc.arg(workspace_id) AND pull_author_id = sqlc.arg(pull_author_id) AND pull > 0;

-- name: UpsertGithubReply :exec
INSERT INTO github_reply (source_id, comment_id, spec_doc_id, finding_key, author, body, url, created_at)
VALUES (sqlc.arg(source_id), sqlc.arg(comment_id), sqlc.arg(spec_doc_id), sqlc.arg(finding_key), sqlc.arg(author), sqlc.arg(body),
        sqlc.arg(url), sqlc.arg(created_at))
ON CONFLICT (source_id, comment_id) DO UPDATE SET body = excluded.body, finding_key = excluded.finding_key, spec_doc_id = excluded.spec_doc_id;

-- name: ListGithubRepliesOfDoc :many
SELECT * FROM github_reply WHERE spec_doc_id = sqlc.arg(spec_doc_id) ORDER BY created_at, comment_id;
