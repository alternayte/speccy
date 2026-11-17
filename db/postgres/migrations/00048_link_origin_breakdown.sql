-- +goose Up
-- A tracker issue a breakdown push created is an external link of the doc, with the origin
-- breakdown (#157).
ALTER TABLE link DROP CONSTRAINT link_origin_check;
ALTER TABLE link ADD CONSTRAINT link_origin_check CHECK (origin IN ('frontmatter', 'rule', 'adopted', 'breakdown'));

-- +goose Down
DELETE FROM link WHERE origin = 'breakdown';
ALTER TABLE link DROP CONSTRAINT link_origin_check;
ALTER TABLE link ADD CONSTRAINT link_origin_check CHECK (origin IN ('frontmatter', 'rule', 'adopted'));
