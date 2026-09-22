-- Deleting a bundle removes everything that hangs off it. The order is children first,
-- because the foreign keys do not cascade.

-- name: DeleteVerificationOutcomesOfBundle :exec
DELETE FROM verification_outcome WHERE run_id IN (SELECT id FROM verification_run WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteVerificationRunsOfBundle :exec
DELETE FROM verification_run WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteHandoffsOfBundle :exec
DELETE FROM handoff WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteVerdictsOfBundle :exec
DELETE FROM verdict WHERE run_id IN (SELECT id FROM review_run WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteAnswersOfBundle :exec
DELETE FROM answer WHERE run_id IN (SELECT id FROM review_run WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteQuestionResultsOfBundle :exec
DELETE FROM question_result WHERE run_id IN (SELECT id FROM review_run WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteQuestionsOfBundle :exec
DELETE FROM question WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteFindingsOfBundle :exec
DELETE FROM finding WHERE run_id IN (SELECT id FROM review_run WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteClaimsOfBundle :exec
DELETE FROM claim WHERE run_id IN (SELECT id FROM review_run WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteRunLinksOfBundle :exec
DELETE FROM run_link WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteReviewRunsOfBundle :exec
DELETE FROM review_run WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteLinksOfBundle :exec
DELETE FROM link WHERE from_bundle_id = sqlc.arg(bundle_id) OR target_bundle_id = sqlc.arg(bundle_id);

-- name: DeleteLinkStatesOfBundle :exec
DELETE FROM link_state WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteThreadMessagesOfBundle :exec
DELETE FROM thread_message_view WHERE thread_id IN (SELECT id FROM thread_view WHERE bundle_id = sqlc.arg(bundle_id));

-- name: DeleteThreadsOfBundle :exec
DELETE FROM thread_view WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteWaiversOfBundle :exec
DELETE FROM waiver_view WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteBundleStatusView :exec
DELETE FROM bundle_status_view WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteBundleReviewers :exec
DELETE FROM bundle_reviewer WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteBundleAuthors :exec
DELETE FROM bundle_author WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteVersionFilesOfBundle :exec
DELETE FROM version_file WHERE version_id IN (SELECT id FROM version WHERE bundle_id = sqlc.arg(bundle_id));

-- name: ClearBundleHead :exec
-- The bundle points at a version, and the version points at the bundle. The head goes first,
-- so neither foreign key holds the other up.
UPDATE bundle SET current_version_id = NULL WHERE id = sqlc.arg(id);

-- name: DeleteVersionsOfBundle :exec
DELETE FROM version WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteOrphanBlobs :exec
DELETE FROM blob WHERE sha256 NOT IN (SELECT sha256 FROM version_file);

-- name: DeleteBundleRow :exec
DELETE FROM bundle WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ThreadIDsOfBundle :many
SELECT id FROM thread_view WHERE bundle_id = sqlc.arg(bundle_id);

-- name: WaiverIDsOfBundle :many
SELECT id FROM waiver_view WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteEventsOfStream :exec
DELETE FROM es_events WHERE stream_id = sqlc.arg(stream_id);

-- name: DeleteStream :exec
DELETE FROM es_streams WHERE stream_id = sqlc.arg(stream_id);

-- name: CountBundlesUsingProfile :one
SELECT COUNT(*) FROM bundle WHERE workspace_id = sqlc.arg(workspace_id) AND profile_key = sqlc.arg(profile_key) AND archived_at IS NULL;

-- name: ListBundlesUsingProfile :many
SELECT id, slug, title FROM bundle
WHERE workspace_id = sqlc.arg(workspace_id) AND profile_key = sqlc.arg(profile_key) AND archived_at IS NULL
ORDER BY slug LIMIT 5;

-- name: DeleteProfileRow :exec
DELETE FROM profile WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteProfileMaintainersOf :exec
DELETE FROM profile_maintainer WHERE profile_id = sqlc.arg(profile_id);

-- name: DeleteProfileVersionsOf :exec
DELETE FROM profile_version WHERE profile_id = sqlc.arg(profile_id);
