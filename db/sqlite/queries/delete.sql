-- Deleting a bundle removes everything that hangs off it. The order is children first,
-- because the foreign keys do not cascade.

-- name: DeleteVerificationOutcomesOfSpecDoc :exec
DELETE FROM verification_outcome WHERE run_id IN (SELECT id FROM verification_run WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteVerificationRunsOfSpecDoc :exec
DELETE FROM verification_run WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteHandoffsOfSpecDoc :exec
DELETE FROM handoff WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteVerdictsOfSpecDoc :exec
DELETE FROM verdict WHERE run_id IN (SELECT id FROM review_run WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteAnswersOfSpecDoc :exec
DELETE FROM answer WHERE run_id IN (SELECT id FROM review_run WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteQuestionResultsOfSpecDoc :exec
DELETE FROM question_result WHERE run_id IN (SELECT id FROM review_run WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteQuestionsOfSpecDoc :exec
DELETE FROM question WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteFindingsOfSpecDoc :exec
DELETE FROM finding WHERE run_id IN (SELECT id FROM review_run WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteClaimsOfSpecDoc :exec
DELETE FROM claim WHERE run_id IN (SELECT id FROM review_run WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteRunLinksOfSpecDoc :exec
DELETE FROM run_link WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteReviewRunsOfSpecDoc :exec
DELETE FROM review_run WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteLinksOfSpecDoc :exec
DELETE FROM link WHERE from_spec_doc_id = sqlc.arg(spec_doc_id) OR target_spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteLinkStatesOfSpecDoc :exec
DELETE FROM link_state WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteThreadMessagesOfSpecDoc :exec
DELETE FROM thread_message_view WHERE thread_id IN (SELECT id FROM thread_view WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: DeleteThreadsOfSpecDoc :exec
DELETE FROM thread_view WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteWaiversOfSpecDoc :exec
DELETE FROM waiver_view WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteSpecDocStatusView :exec
DELETE FROM spec_doc_status_view WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteSpecDocReviewers :exec
DELETE FROM spec_doc_reviewer WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteBundleAuthors :exec
DELETE FROM bundle_author WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteVersionFilesOfSpecDoc :exec
DELETE FROM version_file WHERE version_id IN (SELECT id FROM version WHERE spec_doc_id = sqlc.arg(spec_doc_id));

-- name: ClearSpecDocHead :exec
-- The bundle points at a version, and the version points at the bundle. The head goes first,
-- so neither foreign key holds the other up.
UPDATE spec_doc SET current_version_id = NULL WHERE id = sqlc.arg(id);

-- name: DeleteVersionsOfSpecDoc :exec
DELETE FROM version WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteOrphanBlobs :exec
DELETE FROM blob WHERE sha256 NOT IN (SELECT sha256 FROM version_file);

-- name: DeleteSpecDocRow :exec
DELETE FROM spec_doc WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: CountSpecDocsInBundle :one
-- Every spec doc of the bundle, archived or not.
SELECT COUNT(*) FROM spec_doc WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteShareGuestsOfBundle :exec
DELETE FROM share_guest WHERE bundle_id = sqlc.arg(bundle_id);

-- name: DeleteBundleRow :exec
DELETE FROM bundle WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ThreadIDsOfSpecDoc :many
SELECT id FROM thread_view WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: WaiverIDsOfSpecDoc :many
SELECT id FROM waiver_view WHERE spec_doc_id = sqlc.arg(spec_doc_id);

-- name: DeleteEventsOfStream :exec
DELETE FROM es_events WHERE stream_id = sqlc.arg(stream_id);

-- name: DeleteStream :exec
DELETE FROM es_streams WHERE stream_id = sqlc.arg(stream_id);

-- name: CountSpecDocsUsingProfile :one
SELECT COUNT(*) FROM spec_doc WHERE workspace_id = sqlc.arg(workspace_id) AND profile_key = sqlc.arg(profile_key) AND archived_at IS NULL;

-- name: ListSpecDocsUsingProfile :many
SELECT id, slug, title FROM spec_doc
WHERE workspace_id = sqlc.arg(workspace_id) AND profile_key = sqlc.arg(profile_key) AND archived_at IS NULL
ORDER BY slug LIMIT 5;

-- name: DeleteProfileRow :exec
DELETE FROM profile WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteProfileMaintainersOf :exec
DELETE FROM profile_maintainer WHERE profile_id = sqlc.arg(profile_id);

-- name: DeleteProfileVersionsOf :exec
DELETE FROM profile_version WHERE profile_id = sqlc.arg(profile_id);
