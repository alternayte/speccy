-- name: InsertJob :exec
INSERT INTO job (id, workspace_id, kind, payload, status, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(kind), sqlc.arg(payload), 'queued', sqlc.arg(created_at));

-- name: ClaimJob :one
-- The oldest queued job, or a running job whose lock expired (its worker died).
UPDATE job
SET status = 'running', attempts = attempts + 1, locked_until = sqlc.arg(lock_until)
WHERE id = (
    SELECT id FROM job
    WHERE status = 'queued' OR (status = 'running' AND job.locked_until < sqlc.arg(now))
    ORDER BY created_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: FinishJob :exec
UPDATE job SET status = sqlc.arg(status), last_error = sqlc.arg(last_error), locked_until = NULL
WHERE id = sqlc.arg(id);

-- name: GetCache :one
SELECT result FROM cache_entry WHERE key_hash = sqlc.arg(key_hash);

-- name: PutCache :exec
INSERT INTO cache_entry (key_hash, result, created_at) VALUES (sqlc.arg(key_hash), sqlc.arg(result), sqlc.arg(created_at))
ON CONFLICT (key_hash) DO NOTHING;

-- name: ListMCPConnections :many
SELECT * FROM mcp_connection WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY name;

-- name: GetMCPConnection :one
SELECT * FROM mcp_connection WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: InsertMCPConnection :exec
INSERT INTO mcp_connection (id, workspace_id, name, transport, command_or_url, secret_encrypted, secret_last4,
                            tool_allowlist, is_search, search_tool, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(transport), sqlc.arg(command_or_url),
        sqlc.arg(secret_encrypted), sqlc.arg(secret_last4), sqlc.arg(tool_allowlist), sqlc.arg(is_search),
        sqlc.arg(search_tool), sqlc.arg(created_at));

-- name: UpdateMCPConnection :exec
UPDATE mcp_connection
SET name = sqlc.arg(name), transport = sqlc.arg(transport), command_or_url = sqlc.arg(command_or_url),
    secret_encrypted = sqlc.arg(secret_encrypted), secret_last4 = sqlc.arg(secret_last4),
    tool_allowlist = sqlc.arg(tool_allowlist), is_search = sqlc.arg(is_search), search_tool = sqlc.arg(search_tool)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteMCPConnection :exec
DELETE FROM mcp_connection WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: InsertClaim :exec
INSERT INTO claim (id, run_id, text, label, reason, sources, anchor)
VALUES (sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(text), sqlc.arg(label), sqlc.arg(reason), sqlc.arg(sources), sqlc.arg(anchor));

-- name: ListClaims :many
SELECT * FROM claim WHERE run_id = sqlc.arg(run_id) ORDER BY id;

-- name: UpdateRunProgress :exec
UPDATE review_run SET status = sqlc.arg(status), stage = sqlc.arg(stage) WHERE id = sqlc.arg(id);

-- name: FinishRun :exec
UPDATE review_run
SET status = sqlc.arg(status), stage = sqlc.arg(stage), error = sqlc.arg(error), roles = sqlc.arg(roles),
    prompt_versions = sqlc.arg(prompt_versions), tokens_in = sqlc.arg(tokens_in), tokens_out = sqlc.arg(tokens_out),
    cost_estimate = sqlc.arg(cost_estimate), cache_hits = sqlc.arg(cache_hits), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id);

-- name: RunningRunFor :one
SELECT * FROM review_run
WHERE bundle_id = sqlc.arg(bundle_id) AND kind = 'full' AND status IN ('queued', 'running')
ORDER BY started_at DESC
LIMIT 1;
