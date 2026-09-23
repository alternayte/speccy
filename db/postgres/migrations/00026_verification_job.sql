-- +goose Up
-- A verification run is a job. It exists from the moment it is queued, with the repo and the
-- commit it will read, and its verdict and counts arrive when it ends. status is queued,
-- running, done or failed; error says why a run failed. branch is the branch the SHA was
-- resolved from, or '' for a commit, a folder or a pull request.
ALTER TABLE verification_run ADD COLUMN status text NOT NULL DEFAULT 'done';
ALTER TABLE verification_run ADD COLUMN error text NOT NULL DEFAULT '';
ALTER TABLE verification_run ADD COLUMN branch text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE verification_run DROP COLUMN branch;
ALTER TABLE verification_run DROP COLUMN error;
ALTER TABLE verification_run DROP COLUMN status;
