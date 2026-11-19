-- +goose Up
-- Each queue has its own monthly budget, and spend is kept by queue, person and repo
-- (docs/specs/hosted-operations.md). The old limit becomes the interactive limit, and the pr
-- limit starts at the same value, so an upgrade stops no review.
CREATE TABLE budget_new (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    month        TEXT NOT NULL,
    token_limit  INTEGER,
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    queue        TEXT NOT NULL DEFAULT 'interactive' CHECK (queue IN ('interactive', 'pr')),
    PRIMARY KEY (workspace_id, month, queue)
);
INSERT INTO budget_new (workspace_id, month, queue, token_limit, tokens_used)
SELECT workspace_id, month, 'interactive', token_limit, tokens_used FROM budget;
INSERT INTO budget_new (workspace_id, month, queue, token_limit, tokens_used)
SELECT workspace_id, month, 'pr', token_limit, 0 FROM budget;
DROP TABLE budget;
ALTER TABLE budget_new RENAME TO budget;

CREATE TABLE model_spend (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    month        TEXT NOT NULL,
    queue        TEXT NOT NULL,
    actor        TEXT NOT NULL,
    repo         TEXT NOT NULL,
    tokens       INTEGER NOT NULL DEFAULT 0,
    cost_usd     REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, month, queue, actor, repo)
);

-- cache_entry keeps when a review last read each entry, so an entry no review reads goes.
ALTER TABLE cache_entry ADD COLUMN last_used_at DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00';
UPDATE cache_entry SET last_used_at = created_at;

-- +goose Down
ALTER TABLE cache_entry DROP COLUMN last_used_at;
DROP TABLE model_spend;
CREATE TABLE budget_old (
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    month        TEXT NOT NULL,
    token_limit  INTEGER,
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, month)
);
INSERT INTO budget_old SELECT workspace_id, month, token_limit, tokens_used FROM budget WHERE queue = 'interactive';
DROP TABLE budget;
ALTER TABLE budget_old RENAME TO budget;
