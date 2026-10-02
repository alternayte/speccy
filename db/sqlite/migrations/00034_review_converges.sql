-- +goose Up
-- A doc keeps its build questions from one version to the next (docs/specs/review-converges.md).
-- A question is retired when its cite is gone from the doc, or when a person asks for a fresh
-- set. Until now each version had its own copy of the set, so only the newest copy stays live.
ALTER TABLE question ADD COLUMN retired_at DATETIME;
UPDATE question SET retired_at = strftime('%Y-%m-%d %H:%M:%S+00:00', 'now')
WHERE version_id <> (
    SELECT q2.version_id FROM question q2 JOIN version v ON v.id = q2.version_id
    WHERE q2.spec_doc_id = question.spec_doc_id
    ORDER BY v.number DESC LIMIT 1
);
-- What a full review fixed, left open and found new against the full review before it.
ALTER TABLE verdict ADD COLUMN trend JSONTEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE verdict DROP COLUMN trend;
ALTER TABLE question DROP COLUMN retired_at;
