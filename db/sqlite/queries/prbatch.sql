-- name: InsertPrBatch :exec
INSERT INTO pr_batch (id, workspace_id, status, parallel, stages, again, source, estimate, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(status), sqlc.arg(parallel), sqlc.arg(stages), sqlc.arg(again),
        sqlc.arg(source), sqlc.arg(estimate), sqlc.arg(created_at));

-- name: GetPrBatch :one
SELECT * FROM pr_batch WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetPrBatchStatus :exec
UPDATE pr_batch SET status = sqlc.arg(status), finished_at = sqlc.narg(finished_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListActivePrBatches :many
SELECT * FROM pr_batch WHERE workspace_id = sqlc.arg(workspace_id) AND status IN ('planned', 'running') ORDER BY created_at;

-- name: InsertPrBatchItem :exec
INSERT INTO pr_batch_item (batch_id, position, repo, pull, url, state, reason, head_sha, result, comments, review_url, updated_at)
VALUES (sqlc.arg(batch_id), sqlc.arg(position), sqlc.arg(repo), sqlc.arg(pull), sqlc.arg(url), sqlc.arg(state), sqlc.arg(reason),
        sqlc.arg(head_sha), sqlc.arg(result), sqlc.arg(comments), sqlc.arg(review_url), sqlc.arg(updated_at));

-- name: ListPrBatchItems :many
SELECT * FROM pr_batch_item WHERE batch_id = sqlc.arg(batch_id) ORDER BY position;

-- name: UpdatePrBatchItem :exec
UPDATE pr_batch_item SET state = sqlc.arg(state), reason = sqlc.arg(reason), head_sha = sqlc.arg(head_sha), result = sqlc.arg(result),
    comments = sqlc.arg(comments), removed = sqlc.arg(removed), review_url = sqlc.arg(review_url), updated_at = sqlc.arg(updated_at)
WHERE batch_id = sqlc.arg(batch_id) AND position = sqlc.arg(position);

-- name: StopPrBatchItems :exec
UPDATE pr_batch_item SET state = sqlc.arg(state), reason = sqlc.arg(reason), updated_at = sqlc.arg(updated_at)
WHERE batch_id = sqlc.arg(batch_id) AND state IN ('waiting', 'reviewing');

-- name: HasPrReview :one
SELECT count(*) FROM pr_review
WHERE workspace_id = sqlc.arg(workspace_id) AND repo = sqlc.arg(repo) AND pull = sqlc.arg(pull) AND head_sha = sqlc.arg(head_sha);

-- name: RecordPrReview :exec
INSERT INTO pr_review (workspace_id, repo, pull, head_sha, batch_id, reviewed_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(repo), sqlc.arg(pull), sqlc.arg(head_sha), sqlc.narg(batch_id), sqlc.arg(reviewed_at))
ON CONFLICT (workspace_id, repo, pull, head_sha) DO UPDATE SET batch_id = excluded.batch_id, reviewed_at = excluded.reviewed_at;
