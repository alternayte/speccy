-- name: GetProfileByKey :one
SELECT * FROM profile WHERE workspace_id = sqlc.arg(workspace_id) AND key = sqlc.arg(key);

-- name: ListProfiles :many
SELECT * FROM profile WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY key;

-- name: InsertProfile :exec
INSERT INTO profile (id, workspace_id, key, name, current_version)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(key), sqlc.arg(name), sqlc.arg(current_version));

-- name: SetProfileVersion :exec
UPDATE profile SET name = sqlc.arg(name), current_version = sqlc.arg(current_version) WHERE id = sqlc.arg(id);

-- name: InsertProfileVersion :exec
INSERT INTO profile_version (profile_id, version, yaml, template, origin, created_by, created_at)
VALUES (sqlc.arg(profile_id), sqlc.arg(version), sqlc.arg(yaml), sqlc.arg(template), sqlc.arg(origin),
        sqlc.arg(created_by), sqlc.arg(created_at));

-- name: GetProfileVersion :one
SELECT * FROM profile_version WHERE profile_id = sqlc.arg(profile_id) AND version = sqlc.arg(version);

-- name: InsertRun :exec
INSERT INTO review_run (id, workspace_id, bundle_id, version_id, profile_key, profile_version, kind, status, stage,
                        error, notes, decisions_hash, started_at, finished_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(bundle_id), sqlc.arg(version_id), sqlc.arg(profile_key),
        sqlc.arg(profile_version), sqlc.arg(kind), sqlc.arg(status), sqlc.arg(stage), sqlc.arg(error),
        sqlc.arg(notes), sqlc.arg(decisions_hash), sqlc.arg(started_at), sqlc.narg(finished_at));

-- name: GetRun :one
SELECT * FROM review_run WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: LatestRun :one
SELECT * FROM review_run
WHERE bundle_id = sqlc.arg(bundle_id)
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: LatestRunFor :one
SELECT * FROM review_run
WHERE bundle_id = sqlc.arg(bundle_id) AND version_id = sqlc.arg(version_id)
  AND profile_key = sqlc.arg(profile_key) AND profile_version = sqlc.arg(profile_version)
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: LatestCompleteRun :one
-- REQ-007: the latest finished run of a kind on a version.
SELECT * FROM review_run
WHERE bundle_id = sqlc.arg(bundle_id) AND version_id = sqlc.arg(version_id) AND status = 'complete' AND kind = sqlc.arg(kind)
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: ListRuns :many
SELECT * FROM review_run
WHERE bundle_id = sqlc.arg(bundle_id) AND started_at < sqlc.arg(before)
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: InsertFinding :exec
INSERT INTO finding (id, run_id, check_slug, level, stage, relaxed, anchor, message, evidence, suggestion, waived)
VALUES (sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(check_slug), sqlc.arg(level), sqlc.arg(stage), sqlc.arg(relaxed),
        sqlc.arg(anchor), sqlc.arg(message), sqlc.arg(evidence), sqlc.arg(suggestion), sqlc.arg(waived));

-- name: ListFindings :many
SELECT * FROM finding WHERE run_id = sqlc.arg(run_id) ORDER BY id;

-- name: SetFindingSuggestion :exec
UPDATE finding SET suggestion = sqlc.arg(suggestion) WHERE id = sqlc.arg(id);

-- name: InsertVerdict :exec
INSERT INTO verdict (run_id, result, score, radar, waiver_count, relaxed_count, blocking_finding_ids,
                     items, carried_run_id, carried_findings, sections_changed)
VALUES (sqlc.arg(run_id), sqlc.arg(result), sqlc.arg(score), sqlc.arg(radar), sqlc.arg(waiver_count),
        sqlc.arg(relaxed_count), sqlc.arg(blocking_finding_ids), sqlc.arg(items), sqlc.narg(carried_run_id),
        sqlc.arg(carried_findings), sqlc.arg(sections_changed));

-- name: GetVerdict :one
SELECT * FROM verdict WHERE run_id = sqlc.arg(run_id);

-- name: InsertContentReview :exec
INSERT INTO content_review (id, workspace_id, slug, title, main_doc, profile_key, profile_version, files, result, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(slug), sqlc.arg(title), sqlc.arg(main_doc), sqlc.arg(profile_key),
    sqlc.arg(profile_version), sqlc.arg(files), sqlc.arg(result), sqlc.arg(created_by), sqlc.arg(created_at));

-- name: GetContentReview :one
SELECT * FROM content_review WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteContentReviewsBefore :exec
DELETE FROM content_review WHERE workspace_id = sqlc.arg(workspace_id) AND created_at < sqlc.arg(before);
