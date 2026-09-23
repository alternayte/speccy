-- name: InsertVerificationRun :exec
INSERT INTO verification_run (id, workspace_id, bundle_id, version_id, handoff_id, repo, sha, branch, base_sha, digest,
                              verdict, counts, notes, status, started_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(bundle_id), sqlc.arg(version_id), sqlc.narg(handoff_id),
        sqlc.arg(repo), sqlc.arg(sha), sqlc.arg(branch), '', '', '', sqlc.arg(counts), sqlc.arg(notes), 'queued',
        sqlc.arg(started_by), sqlc.arg(created_at));

-- name: StartVerificationRun :exec
UPDATE verification_run SET status = 'running' WHERE id = sqlc.arg(id);

-- name: FinishVerificationRun :exec
UPDATE verification_run
SET status = 'done', base_sha = sqlc.arg(base_sha), digest = sqlc.arg(digest), verdict = sqlc.arg(verdict),
    counts = sqlc.arg(counts), notes = sqlc.arg(notes)
WHERE id = sqlc.arg(id);

-- name: FailVerificationRun :exec
UPDATE verification_run SET status = 'failed', error = sqlc.arg(error) WHERE id = sqlc.arg(id);

-- name: InsertVerificationOutcome :exec
INSERT INTO verification_outcome (id, run_id, trace_id, outcome, level, blocks, waived, provenance, note, targets, judgement)
VALUES (sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(trace_id), sqlc.arg(outcome), sqlc.arg(level), sqlc.arg(blocks),
        sqlc.arg(waived), sqlc.arg(provenance), sqlc.arg(note), sqlc.arg(targets), sqlc.arg(judgement));

-- name: GetVerificationRun :one
SELECT * FROM verification_run WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListVerificationRuns :many
SELECT * FROM verification_run WHERE bundle_id = sqlc.arg(bundle_id) ORDER BY created_at DESC, id;

-- name: ListVerificationOutcomes :many
SELECT * FROM verification_outcome WHERE run_id = sqlc.arg(run_id) ORDER BY trace_id;

-- name: LatestVerificationSHA :one
SELECT sha FROM verification_run
WHERE bundle_id = sqlc.arg(bundle_id) AND repo = sqlc.arg(repo) AND sha <> '' AND status = 'done'
ORDER BY created_at DESC, id LIMIT 1;

-- name: StaleVerificationRuns :exec
UPDATE verification_run SET stale = true WHERE bundle_id = sqlc.arg(bundle_id) AND version_id <> sqlc.arg(version_id);

-- name: ListWorkspaceVerificationRuns :many
SELECT * FROM verification_run WHERE workspace_id = sqlc.arg(workspace_id) AND status = 'done' ORDER BY created_at DESC;
