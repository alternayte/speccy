-- name: UpsertThreadView :exec
INSERT INTO thread_view (id, workspace_id, bundle_id, profile_key, anchor_kind, anchor, addressed_to, title, blocking,
                         status, created_by, created_at, last_message_at, message_count, handoff_id, handoff_version)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.narg(bundle_id), sqlc.arg(profile_key), sqlc.arg(anchor_kind),
        sqlc.arg(anchor), sqlc.arg(addressed_to), sqlc.arg(title), sqlc.arg(blocking), sqlc.arg(status),
        sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(last_message_at), sqlc.arg(message_count),
        sqlc.narg(handoff_id), sqlc.arg(handoff_version))
ON CONFLICT (id) DO UPDATE SET blocking = excluded.blocking, status = excluded.status,
    last_message_at = excluded.last_message_at, message_count = excluded.message_count;

-- name: GetThreadView :one
SELECT * FROM thread_view WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListBundleThreads :many
SELECT * FROM thread_view WHERE bundle_id = sqlc.arg(bundle_id) ORDER BY status DESC, last_message_at DESC;

-- name: ListProfileThreads :many
SELECT * FROM thread_view WHERE workspace_id = sqlc.arg(workspace_id) AND profile_key = sqlc.arg(profile_key)
ORDER BY status DESC, last_message_at DESC;

-- name: CountOpenBlockingThreads :one
SELECT COUNT(*) FROM thread_view WHERE bundle_id = sqlc.arg(bundle_id) AND blocking AND status = 'open';

-- name: InsertThreadMessage :exec
INSERT INTO thread_message_view (id, thread_id, seq, author_kind, author_id, author_name, body, sources, decision, created_at)
VALUES (sqlc.arg(id), sqlc.arg(thread_id), sqlc.arg(seq), sqlc.arg(author_kind), sqlc.arg(author_id), sqlc.arg(author_name),
        sqlc.arg(body), sqlc.arg(sources), sqlc.arg(decision), sqlc.arg(created_at));

-- name: SetMessageDecision :exec
UPDATE thread_message_view SET decision = sqlc.arg(decision) WHERE id = sqlc.arg(id);

-- name: ListThreadMessages :many
SELECT * FROM thread_message_view WHERE thread_id = sqlc.arg(thread_id) ORDER BY seq;

-- name: ListMessagesSince :many
-- Messages in threads of the workspace after a time, newest first, for the inbox.
SELECT m.*, t.bundle_id, t.profile_key, t.title
FROM thread_message_view m JOIN thread_view t ON t.id = m.thread_id
WHERE t.workspace_id = sqlc.arg(workspace_id) AND m.created_at > sqlc.arg(since)
ORDER BY m.created_at DESC LIMIT 500;

-- name: UpsertWaiverView :exec
INSERT INTO waiver_view (id, workspace_id, bundle_id, check_slug, level, section_path, section_hash, reason, status,
                         requested_by, approvals, decided_by, created_at, updated_at, scope, trace_id, repo)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(bundle_id), sqlc.arg(check_slug), sqlc.arg(level),
        sqlc.arg(section_path), sqlc.arg(section_hash), sqlc.arg(reason), sqlc.arg(status), sqlc.arg(requested_by),
        sqlc.arg(approvals), sqlc.arg(decided_by), sqlc.arg(created_at), sqlc.arg(updated_at),
        sqlc.arg(scope), sqlc.arg(trace_id), sqlc.arg(repo))
ON CONFLICT (id) DO UPDATE SET status = excluded.status, approvals = excluded.approvals,
    decided_by = excluded.decided_by, updated_at = excluded.updated_at;

-- name: GetWaiverView :one
SELECT * FROM waiver_view WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListBundleWaivers :many
SELECT * FROM waiver_view WHERE bundle_id = sqlc.arg(bundle_id) ORDER BY created_at DESC;

-- name: ListVerificationWaivers :many
SELECT * FROM waiver_view
WHERE bundle_id = sqlc.arg(bundle_id) AND scope = 'verify' AND repo = sqlc.arg(repo) AND status = 'approved';

-- name: ListWorkspaceWaivers :many
SELECT * FROM waiver_view WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY created_at DESC;

-- name: UpsertBundleStatusView :exec
INSERT INTO bundle_status_view (bundle_id, status, approvals, approved_version, review_requested_at, approved_at, updated_at)
VALUES (sqlc.arg(bundle_id), sqlc.arg(status), sqlc.arg(approvals), sqlc.narg(approved_version),
        sqlc.narg(review_requested_at), sqlc.narg(approved_at), sqlc.arg(updated_at))
ON CONFLICT (bundle_id) DO UPDATE SET status = excluded.status, approvals = excluded.approvals,
    approved_version = excluded.approved_version, review_requested_at = excluded.review_requested_at,
    approved_at = excluded.approved_at, updated_at = excluded.updated_at;

-- name: GetBundleStatusView :one
SELECT * FROM bundle_status_view WHERE bundle_id = sqlc.arg(bundle_id);

-- name: ListBundleStatusViews :many
SELECT s.* FROM bundle_status_view s JOIN bundle b ON b.id = s.bundle_id WHERE b.workspace_id = sqlc.arg(workspace_id);

-- name: InsertBundleReviewer :exec
INSERT INTO bundle_reviewer (bundle_id, user_id) VALUES (sqlc.arg(bundle_id), sqlc.arg(user_id)) ON CONFLICT DO NOTHING;

-- name: ListReviewerBundles :many
SELECT bundle_id FROM bundle_reviewer WHERE user_id = sqlc.arg(user_id);

-- name: ListAuthorBundles :many
SELECT bundle_id FROM bundle_author WHERE user_id = sqlc.arg(user_id);

-- name: ListProfileMaintainers :many
SELECT user_id FROM profile_maintainer WHERE profile_id = sqlc.arg(profile_id) ORDER BY user_id;

-- name: DeleteProfileMaintainers :exec
DELETE FROM profile_maintainer WHERE profile_id = sqlc.arg(profile_id);

-- name: InsertProfileMaintainer :exec
INSERT INTO profile_maintainer (profile_id, user_id) VALUES (sqlc.arg(profile_id), sqlc.arg(user_id)) ON CONFLICT DO NOTHING;

-- name: IsProfileMaintainer :one
SELECT EXISTS (SELECT 1 FROM profile_maintainer pm JOIN profile p ON p.id = pm.profile_id
               WHERE p.workspace_id = sqlc.arg(workspace_id) AND p.key = sqlc.arg(key) AND pm.user_id = sqlc.arg(user_id)) AS maintainer;

-- name: GetUserState :one
SELECT * FROM user_state WHERE user_id = sqlc.arg(user_id);

-- name: SetInboxSeen :exec
INSERT INTO user_state (user_id, inbox_seen_at) VALUES (sqlc.arg(user_id), sqlc.arg(inbox_seen_at))
ON CONFLICT (user_id) DO UPDATE SET inbox_seen_at = excluded.inbox_seen_at;

-- name: ListFullRunsSince :many
SELECT * FROM review_run WHERE workspace_id = sqlc.arg(workspace_id) AND kind = 'full' AND status IN ('complete', 'failed')
  AND finished_at > sqlc.arg(since)
ORDER BY finished_at DESC LIMIT 200;

-- name: ListAllRuns :many
-- For insights: every finished run of the workspace, oldest first.
SELECT r.*, v.result AS verdict_result
FROM review_run r LEFT JOIN verdict v ON v.run_id = r.id
WHERE r.workspace_id = sqlc.arg(workspace_id) AND r.status = 'complete'
ORDER BY r.started_at;

-- name: CountFindingsByCheck :many
-- For insights: failing checks in the latest completed run of each bundle.
SELECT f.check_slug, COUNT(*) AS n
FROM finding f
WHERE f.run_id IN (
    SELECT (SELECT r2.id FROM review_run r2 WHERE r2.bundle_id = b.id AND r2.status = 'complete'
            ORDER BY r2.started_at DESC LIMIT 1)
    FROM bundle b WHERE b.workspace_id = sqlc.arg(workspace_id) AND b.archived_at IS NULL AND b.profile_key = sqlc.arg(profile_key)
)
GROUP BY f.check_slug ORDER BY n DESC LIMIT 10;

-- name: CountAuthorMessagesSince :one
-- REQ-086: a guest's posts in the last hour.
SELECT COUNT(*) FROM thread_message_view WHERE author_id = sqlc.arg(author_id) AND created_at > sqlc.arg(since);

-- name: GetFinding :one
SELECT * FROM finding WHERE id = sqlc.arg(id);

-- name: ListSupersedesLinks :many
SELECT * FROM link WHERE workspace_id = sqlc.arg(workspace_id) AND kind = 'supersedes' AND target_bundle_id IS NOT NULL;

-- name: ListProfileVersions :many
SELECT version, created_by, created_at, origin FROM profile_version WHERE profile_id = sqlc.arg(profile_id) ORDER BY version DESC LIMIT 50;

-- name: IsAnyMaintainer :one
SELECT EXISTS (SELECT 1 FROM profile_maintainer pm JOIN profile p ON p.id = pm.profile_id
               WHERE p.workspace_id = sqlc.arg(workspace_id) AND pm.user_id = sqlc.arg(user_id)) AS maintainer;

-- name: ListBuildThreads :many
SELECT * FROM thread_view WHERE workspace_id = sqlc.arg(workspace_id) AND handoff_id IS NOT NULL;

-- name: MarkInboxItemRead :exec
INSERT INTO inbox_read (user_id, item_key, read_at) VALUES (sqlc.arg(user_id), sqlc.arg(item_key), sqlc.arg(read_at))
ON CONFLICT (user_id, item_key) DO NOTHING;

-- name: ListInboxRead :many
SELECT item_key FROM inbox_read WHERE user_id = sqlc.arg(user_id) AND read_at > sqlc.arg(since);
