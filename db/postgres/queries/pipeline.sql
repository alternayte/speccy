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

-- name: ListActiveJobs :many
-- The jobs that wait for a worker or run in one. A local owner that starts finds only jobs
-- that a process before it left, and an owner that wants to exit waits until there is none.
SELECT * FROM job WHERE status IN ('queued', 'running') ORDER BY created_at;

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
                            tool_allowlist, is_search, search_tool, created_at, hosts, fetch_tool)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(transport), sqlc.arg(command_or_url),
        sqlc.arg(secret_encrypted), sqlc.arg(secret_last4), sqlc.arg(tool_allowlist), sqlc.arg(is_search),
        sqlc.arg(search_tool), sqlc.arg(created_at), sqlc.arg(hosts), sqlc.arg(fetch_tool));

-- name: UpdateMCPConnection :exec
UPDATE mcp_connection
SET name = sqlc.arg(name), transport = sqlc.arg(transport), command_or_url = sqlc.arg(command_or_url),
    secret_encrypted = sqlc.arg(secret_encrypted), secret_last4 = sqlc.arg(secret_last4),
    tool_allowlist = sqlc.arg(tool_allowlist), is_search = sqlc.arg(is_search), search_tool = sqlc.arg(search_tool),
    hosts = sqlc.arg(hosts), fetch_tool = sqlc.arg(fetch_tool)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteMCPConnection :exec
DELETE FROM mcp_connection WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: InsertClaim :exec
INSERT INTO claim (id, run_id, text, label, reason, class, sources, anchor)
VALUES (sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(text), sqlc.arg(label), sqlc.arg(reason), sqlc.arg(class), sqlc.arg(sources), sqlc.arg(anchor));

-- name: ListClaims :many
SELECT * FROM claim WHERE run_id = sqlc.arg(run_id) ORDER BY id;

-- name: UpdateRunProgress :exec
UPDATE review_run SET status = sqlc.arg(status), stage = sqlc.arg(stage) WHERE id = sqlc.arg(id);

-- name: FinishRun :exec
UPDATE review_run
SET status = sqlc.arg(status), stage = sqlc.arg(stage), error = sqlc.arg(error), roles = sqlc.arg(roles),
    prompt_versions = sqlc.arg(prompt_versions), tokens_in = sqlc.arg(tokens_in), tokens_out = sqlc.arg(tokens_out),
    cost_estimate = sqlc.arg(cost_estimate), cache_hits = sqlc.arg(cache_hits), notes = sqlc.arg(notes),
    stages = sqlc.arg(stages), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id);

-- name: FailActiveRun :exec
-- Ends a run that its job left queued or running. A run that reached an end stays as it is.
UPDATE review_run SET status = 'failed', error = sqlc.arg(error), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id) AND status IN ('queued', 'running');

-- name: RunningRunFor :one
SELECT * FROM review_run
WHERE spec_doc_id = sqlc.arg(spec_doc_id) AND kind = 'full' AND status IN ('queued', 'running')
ORDER BY started_at DESC
LIMIT 1;

-- name: StartRunExecution :exec
UPDATE review_run
SET status = 'running', stage = sqlc.arg(stage), profile_version = sqlc.arg(profile_version)
WHERE id = sqlc.arg(id);

-- name: GetRunByID :one
SELECT * FROM review_run WHERE id = sqlc.arg(id);

-- name: ListLiveQuestions :many
-- The build questions a doc keeps: every question that is not retired.
SELECT * FROM question WHERE spec_doc_id = sqlc.arg(spec_doc_id) AND retired_at IS NULL ORDER BY number;

-- name: ListDocQuestions :many
-- Every question the doc ever had, the retired ones too. A new question takes the next number.
SELECT * FROM question WHERE spec_doc_id = sqlc.arg(spec_doc_id) ORDER BY number;

-- name: ListRunQuestions :many
-- The questions a run answered, which may be retired since.
SELECT q.* FROM question q JOIN question_result r ON r.question_id = q.id
WHERE r.run_id = sqlc.arg(run_id) ORDER BY q.number;

-- name: RetireQuestion :exec
UPDATE question SET retired_at = sqlc.arg(retired_at) WHERE id = sqlc.arg(id);

-- name: RetireQuestions :exec
UPDATE question SET retired_at = sqlc.arg(retired_at) WHERE spec_doc_id = sqlc.arg(spec_doc_id) AND retired_at IS NULL;

-- name: SetSpecDocFresh :exec
-- The next full review of the doc judges each rubric check from nothing.
UPDATE spec_doc SET fresh_at = sqlc.arg(fresh_at) WHERE id = sqlc.arg(id);

-- name: UpdateQuestionCites :exec
UPDATE question SET cites = sqlc.arg(cites), level = sqlc.arg(level), anchor = sqlc.arg(anchor) WHERE id = sqlc.arg(id);

-- name: LatestQuestionResult :one
-- The result of a question in the last finished run that asked it.
SELECT r.* FROM question_result r JOIN review_run run ON run.id = r.run_id
WHERE r.question_id = sqlc.arg(question_id) AND run.status = 'complete'
ORDER BY run.started_at DESC, run.id DESC
LIMIT 1;

-- name: ListQuestionAnswers :many
SELECT * FROM answer WHERE run_id = sqlc.arg(run_id) AND question_id = sqlc.arg(question_id) ORDER BY reader_role;

-- name: InsertQuestion :exec
INSERT INTO question (id, workspace_id, spec_doc_id, version_id, number, text, level, cites, anchor, input_hash)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(spec_doc_id), sqlc.arg(version_id), sqlc.arg(number),
        sqlc.arg(text), sqlc.arg(level), sqlc.arg(cites), sqlc.arg(anchor), sqlc.arg(input_hash));

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
DELETE FROM link WHERE from_spec_doc_id = sqlc.arg(from_spec_doc_id);

-- name: InsertLink :exec
INSERT INTO link (id, workspace_id, from_spec_doc_id, kind, target_kind, target_spec_doc_id, target_ref, origin, target_url)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(from_spec_doc_id), sqlc.arg(kind), sqlc.arg(target_kind),
        sqlc.arg(target_spec_doc_id), sqlc.arg(target_ref), sqlc.arg(origin), sqlc.arg(target_url));

-- name: ListLinksFrom :many
SELECT * FROM link WHERE from_spec_doc_id = sqlc.arg(from_spec_doc_id) ORDER BY kind, target_ref;

-- name: ListLinksTo :many
SELECT * FROM link WHERE target_spec_doc_id = sqlc.arg(target_spec_doc_id) ORDER BY kind, from_spec_doc_id;

-- name: InsertRunLink :exec
INSERT INTO run_link (run_id, spec_doc_id, version_id) VALUES (sqlc.arg(run_id), sqlc.arg(spec_doc_id), sqlc.arg(version_id));

-- name: ListRunLinks :many
SELECT * FROM run_link WHERE run_id = sqlc.arg(run_id) ORDER BY spec_doc_id;

-- name: DeleteLinkStates :exec
DELETE FROM link_state WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: InsertLinkState :exec
INSERT INTO link_state (spec_doc_id, target_ref, state, reason, checked_ref, checked_at)
VALUES (sqlc.arg(spec_doc_id), sqlc.arg(target_ref), sqlc.arg(state), sqlc.arg(reason), sqlc.arg(checked_ref), sqlc.arg(checked_at));

-- name: ListLinkStates :many
SELECT * FROM link_state WHERE spec_doc_id = sqlc.arg(spec_doc_id) ORDER BY target_ref;
