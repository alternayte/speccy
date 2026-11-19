-- name: GetNotifyWebhook :one
SELECT * FROM notify_webhook WHERE workspace_id = sqlc.arg(workspace_id);

-- name: SetNotifyWebhook :exec
INSERT INTO notify_webhook (workspace_id, kind, url_encrypted, url_last4, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(kind), sqlc.arg(url_encrypted), sqlc.arg(url_last4), sqlc.arg(updated_at))
ON CONFLICT (workspace_id) DO UPDATE SET kind = excluded.kind, url_encrypted = excluded.url_encrypted,
    url_last4 = excluded.url_last4, updated_at = excluded.updated_at;

-- name: DeleteNotifyWebhook :exec
DELETE FROM notify_webhook WHERE workspace_id = sqlc.arg(workspace_id);
