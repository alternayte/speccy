-- +goose Up
-- A verdict keeps its scored items, so a later lint run can carry the AI items of a full run.
-- A lint run's verdict names the full run it carried findings from, the findings it carried
-- (with whether a waiver covers each), and how many sections changed since that run. The
-- finding rows stay in the full run: a carried finding keeps its ID.
ALTER TABLE verdict ADD COLUMN items jsonb NOT NULL DEFAULT '[]';
ALTER TABLE verdict ADD COLUMN carried_run_id uuid;
ALTER TABLE verdict ADD COLUMN carried_findings jsonb NOT NULL DEFAULT '[]';
ALTER TABLE verdict ADD COLUMN sections_changed bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE verdict DROP COLUMN sections_changed;
ALTER TABLE verdict DROP COLUMN carried_findings;
ALTER TABLE verdict DROP COLUMN carried_run_id;
ALTER TABLE verdict DROP COLUMN items;
