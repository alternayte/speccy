-- name: ListBackends :many
SELECT * FROM model_backend WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY name;

-- name: GetBackend :one
SELECT * FROM model_backend WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: InsertBackend :exec
INSERT INTO model_backend (id, workspace_id, kind, name, config, secret_encrypted, secret_last4, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(config),
        sqlc.arg(secret_encrypted), sqlc.arg(secret_last4), sqlc.arg(created_at));

-- name: UpdateBackend :exec
UPDATE model_backend
SET name = sqlc.arg(name), config = sqlc.arg(config), secret_encrypted = sqlc.arg(secret_encrypted),
    secret_last4 = sqlc.arg(secret_last4)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteBackend :execrows
DELETE FROM model_backend WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: CountAssignmentsForBackend :one
SELECT COUNT(*) FROM role_assignment WHERE workspace_id = sqlc.arg(workspace_id) AND backend_id = sqlc.arg(backend_id);

-- name: ListAssignments :many
SELECT * FROM role_assignment WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY role;

-- name: GetAssignment :one
SELECT * FROM role_assignment WHERE workspace_id = sqlc.arg(workspace_id) AND role = sqlc.arg(role);

-- name: UpsertAssignment :exec
INSERT INTO role_assignment (workspace_id, role, backend_id, model, price_in_per_mtok, price_out_per_mtok)
VALUES (sqlc.arg(workspace_id), sqlc.arg(role), sqlc.arg(backend_id), sqlc.arg(model), sqlc.arg(price_in_per_mtok),
        sqlc.arg(price_out_per_mtok))
ON CONFLICT (workspace_id, role) DO UPDATE
SET backend_id = excluded.backend_id, model = excluded.model, price_in_per_mtok = excluded.price_in_per_mtok,
    price_out_per_mtok = excluded.price_out_per_mtok;

-- name: DeleteAssignment :exec
DELETE FROM role_assignment WHERE workspace_id = sqlc.arg(workspace_id) AND role = sqlc.arg(role);

-- name: GetBudget :one
SELECT * FROM budget WHERE workspace_id = sqlc.arg(workspace_id) AND month = sqlc.arg(month);

-- name: LatestBudget :one
SELECT * FROM budget WHERE workspace_id = sqlc.arg(workspace_id) ORDER BY month DESC LIMIT 1;

-- name: InsertBudget :exec
INSERT INTO budget (workspace_id, month, token_limit, tokens_used)
VALUES (sqlc.arg(workspace_id), sqlc.arg(month), sqlc.narg(token_limit), 0)
ON CONFLICT (workspace_id, month) DO NOTHING;

-- name: SetBudgetLimit :exec
UPDATE budget SET token_limit = sqlc.narg(token_limit) WHERE workspace_id = sqlc.arg(workspace_id) AND month = sqlc.arg(month);

-- name: AddBudgetTokens :exec
UPDATE budget SET tokens_used = tokens_used + sqlc.arg(tokens)
WHERE workspace_id = sqlc.arg(workspace_id) AND month = sqlc.arg(month);
