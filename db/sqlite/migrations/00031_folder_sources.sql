-- +goose Up
-- A source is always a repo, a branch and a folder. A one-doc source becomes a source for the
-- doc's folder, and the profile a person picked for the doc becomes its adopted type. A one-doc
-- source whose folder another source already reads goes: that source takes its doc over. Of two
-- one-doc sources in one folder, the first stays.
INSERT INTO adopted_type (source_id, path, profile)
SELECT id, path, profile FROM github_source WHERE is_file AND profile <> ''
ON CONFLICT DO NOTHING;
DELETE FROM github_source WHERE id IN (
    SELECT f.id FROM github_source f JOIN github_source o
      ON o.workspace_id = f.workspace_id AND o.repo = f.repo AND o.branch = f.branch AND o.id <> f.id
     AND COALESCE(NULLIF(rtrim(rtrim(o.path, replace(o.path, '/', '')), '/'), ''), '.') = COALESCE(NULLIF(rtrim(rtrim(f.path, replace(f.path, '/', '')), '/'), ''), '.')
     AND o.is_file AND CAST(o.id AS TEXT) < CAST(f.id AS TEXT)
    WHERE f.is_file);
DELETE FROM github_source WHERE id IN (
    SELECT f.id FROM github_source f JOIN github_source o
      ON o.workspace_id = f.workspace_id AND o.repo = f.repo AND o.branch = f.branch AND NOT o.is_file
     AND o.path = COALESCE(NULLIF(rtrim(rtrim(f.path, replace(f.path, '/', '')), '/'), ''), '.')
    WHERE f.is_file);
UPDATE github_source SET path = COALESCE(NULLIF(rtrim(rtrim(path, replace(path, '/', '')), '/'), ''), '.') WHERE is_file;
ALTER TABLE github_source DROP COLUMN is_file;
ALTER TABLE github_source DROP COLUMN profile;

-- +goose Down
ALTER TABLE github_source ADD COLUMN is_file BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE github_source ADD COLUMN profile TEXT NOT NULL DEFAULT '';
