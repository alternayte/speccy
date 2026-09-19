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
    cost_estimate = sqlc.arg(cost_estimate), cache_hits = sqlc.arg(cache_hits), notes = sqlc.arg(notes),
    finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id);

-- name: RunningRunFor :one
SELECT * FROM review_run
WHERE bundle_id = sqlc.arg(bundle_id) AND kind = 'full' AND status IN ('queued', 'running')
ORDER BY started_at DESC
LIMIT 1;

-- name: StartRunExecution :exec
UPDATE review_run
SET status = 'running', stage = sqlc.arg(stage), profile_version = sqlc.arg(profile_version)
WHERE id = sqlc.arg(id);

-- name: GetRunByID :one
SELECT * FROM review_run WHERE id = sqlc.arg(id);

-- name: ListQuestions :many
SELECT * FROM question WHERE version_id = sqlc.arg(version_id) ORDER BY number;

-- name: InsertQuestion :exec
INSERT INTO question (id, workspace_id, bundle_id, version_id, number, text, level, cites, anchor, input_hash)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(bundle_id), sqlc.arg(version_id), sqlc.arg(number),
        sqlc.arg(text), sqlc.arg(level), sqlc.arg(cites), sqlc.arg(anchor), sqlc.arg(input_hash));

-- name: ListQuestionsByInput :many
-- REQ-047: the questions of an earlier version of the bundle with the same content.
SELECT q.* FROM question q
WHERE q.bundle_id = sqlc.arg(bundle_id) AND q.input_hash = sqlc.arg(input_hash) AND q.input_hash <> ''
  AND q.version_id = (SELECT q2.version_id FROM question q2 WHERE q2.bundle_id = sqlc.arg(bundle_id)
                      AND q2.input_hash = sqlc.arg(input_hash) LIMIT 1)
ORDER BY q.number;

-- name: InsertAnswer :exec
INSERT INTO answer (question_id, run_id, reader_role, model_fingerprint, answer, quotes, quotes_found)
VALUES (sqlc.arg(question_id), sqlc.arg(run_id), sqlc.arg(reader_role), sqlc.arg(model_fingerprint),
        sqlc.arg(answer), sqlc.arg(quotes), sqlc.arg(quotes_found));

-- name: ListAnswers :many
SELECT * FROM answer WHERE run_id = sqlc.arg(run_id) ORDER BY question_id, reader_role;

-- name: InsertQuestionResult :exec
INSERT INTO question_result (run_id, question_id, result, groups)
VALUES (sqlc.arg(run_id), sqlc.arg(question_id), sqlc.arg(result), sqlc.arg(groups));

-- name: ListQuestionResults :many
SELECT * FROM question_result WHERE run_id = sqlc.arg(run_id);

-- name: DeleteLinksFrom :exec
DELETE FROM link WHERE from_bundle_id = sqlc.arg(from_bundle_id);

-- name: InsertLink :exec
INSERT INTO link (id, workspace_id, from_bundle_id, kind, target_kind, target_bundle_id, target_ref, origin)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(from_bundle_id), sqlc.arg(kind), sqlc.arg(target_kind),
        sqlc.arg(target_bundle_id), sqlc.arg(target_ref), sqlc.arg(origin));

-- name: ListLinksFrom :many
SELECT * FROM link WHERE from_bundle_id = sqlc.arg(from_bundle_id) ORDER BY kind, target_ref;

-- name: ListLinksTo :many
SELECT * FROM link WHERE target_bundle_id = sqlc.arg(target_bundle_id) ORDER BY kind, from_bundle_id;

-- name: InsertRunLink :exec
INSERT INTO run_link (run_id, bundle_id, version_id) VALUES (sqlc.arg(run_id), sqlc.arg(bundle_id), sqlc.arg(version_id));

-- name: ListRunLinks :many
SELECT * FROM run_link WHERE run_id = sqlc.arg(run_id) ORDER BY bundle_id;
