-- +goose Up
-- Each queue has its own monthly budget, and spend is kept by queue, person and repo
-- (docs/specs/hosted-operations.md). The old limit becomes the interactive limit, and the pr
-- limit starts at the same value, so an upgrade stops no review.
ALTER TABLE budget ADD COLUMN queue text NOT NULL DEFAULT 'interactive' CHECK (queue IN ('interactive', 'pr'));
ALTER TABLE budget DROP CONSTRAINT budget_pkey;
ALTER TABLE budget ADD PRIMARY KEY (workspace_id, month, queue);
INSERT INTO budget (workspace_id, month, token_limit, tokens_used, queue)
SELECT workspace_id, month, token_limit, 0, 'pr' FROM budget WHERE queue = 'interactive';

CREATE TABLE model_spend (
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    month        text NOT NULL,
    queue        text NOT NULL,
    actor        text NOT NULL,
    repo         text NOT NULL,
    tokens       bigint NOT NULL DEFAULT 0,
    cost_usd     double precision NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, month, queue, actor, repo)
);

-- cache_entry keeps when a review last read each entry, so an entry no review reads goes.
ALTER TABLE cache_entry ADD COLUMN last_used_at timestamp with time zone;
UPDATE cache_entry SET last_used_at = created_at;
ALTER TABLE cache_entry ALTER COLUMN last_used_at SET NOT NULL;

-- +goose Down
ALTER TABLE cache_entry DROP COLUMN last_used_at;
DROP TABLE model_spend;
DELETE FROM budget WHERE queue = 'pr';
ALTER TABLE budget DROP CONSTRAINT budget_pkey;
ALTER TABLE budget ADD PRIMARY KEY (workspace_id, month);
ALTER TABLE budget DROP COLUMN queue;
