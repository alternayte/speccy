-- +goose Up
-- A verification waiver excuses one trace ID in one code repo. It never goes in the doc's
-- sidecar, because the sidecar travels with the doc into every build of it.
ALTER TABLE waiver_view ADD COLUMN scope text NOT NULL DEFAULT 'check';
ALTER TABLE waiver_view ADD COLUMN trace_id text NOT NULL DEFAULT '';
ALTER TABLE waiver_view ADD COLUMN repo text NOT NULL DEFAULT '';
CREATE INDEX waiver_view_verify ON waiver_view (bundle_id, scope, repo, trace_id);

-- +goose Down
DROP INDEX waiver_view_verify;
ALTER TABLE waiver_view DROP COLUMN repo;
ALTER TABLE waiver_view DROP COLUMN trace_id;
ALTER TABLE waiver_view DROP COLUMN scope;
