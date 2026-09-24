-- +goose Up
-- A person who can edit a doc withdraws an approved Acknowledgement: the waiver stream records
-- WaiverWithdrawn, and the view shows the status withdrawn.
ALTER TABLE waiver_view DROP CONSTRAINT waiver_view_status_check;
ALTER TABLE waiver_view ADD CONSTRAINT waiver_view_status_check
    CHECK (status IN ('requested', 'approved', 'rejected', 'invalidated', 'withdrawn'));

-- +goose Down
ALTER TABLE waiver_view DROP CONSTRAINT waiver_view_status_check;
UPDATE waiver_view SET status = 'invalidated' WHERE status = 'withdrawn';
ALTER TABLE waiver_view ADD CONSTRAINT waiver_view_status_check
    CHECK (status IN ('requested', 'approved', 'rejected', 'invalidated'));
