-- name: GetBreakdown :one
SELECT * FROM breakdown WHERE workspace_id = sqlc.arg(workspace_id) AND spec_doc_id = sqlc.arg(spec_doc_id);

-- name: GetBreakdownByID :one
SELECT * FROM breakdown WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: InsertBreakdown :exec
INSERT INTO breakdown (id, workspace_id, spec_doc_id, version_id, verdict, acknowledged, revision, body, created_by,
                       created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(spec_doc_id), sqlc.arg(version_id), sqlc.arg(verdict),
        sqlc.arg(acknowledged), 1, sqlc.arg(body), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at));

-- name: UpdateBreakdown :execrows
-- The edit applies only when the caller read the newest revision.
UPDATE breakdown
SET version_id = sqlc.arg(version_id), verdict = sqlc.arg(verdict), acknowledged = sqlc.arg(acknowledged),
    body = sqlc.arg(body), revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(based_on);

-- name: DeleteBreakdownOfSpecDoc :exec
DELETE FROM breakdown WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: InsertBreakdownIssue :exec
INSERT INTO breakdown_issue (id, workspace_id, spec_doc_id, breakdown_id, kind, item_id, tracker, issue_key, issue_url,
                             issue_ref, version_id, handoff_id, trace_ids, pushed_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(spec_doc_id), sqlc.arg(breakdown_id), sqlc.arg(kind),
        sqlc.arg(item_id), sqlc.arg(tracker), sqlc.arg(issue_key), sqlc.arg(issue_url), sqlc.arg(issue_ref),
        sqlc.arg(version_id), sqlc.arg(handoff_id), sqlc.arg(trace_ids), sqlc.arg(pushed_by), sqlc.arg(created_at));

-- name: ListBreakdownIssues :many
SELECT * FROM breakdown_issue WHERE breakdown_id = sqlc.arg(breakdown_id) ORDER BY created_at, item_id;

-- name: FindBreakdownIssue :many
-- A work item by its issue key or URL, across the workspace.
SELECT * FROM breakdown_issue
WHERE workspace_id = sqlc.arg(workspace_id) AND (issue_key = sqlc.arg(ref) OR issue_url = sqlc.arg(ref));

-- name: DeleteBreakdownIssuesOfSpecDoc :exec
DELETE FROM breakdown_issue WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: ListBreakdownIssuesOfSpecDoc :many
SELECT * FROM breakdown_issue WHERE spec_doc_id = sqlc.arg(spec_doc_id) ORDER BY created_at, item_id;
