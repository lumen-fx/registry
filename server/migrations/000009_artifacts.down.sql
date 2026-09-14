DROP TABLE IF EXISTS artifacts;

ALTER TABLE releases DROP COLUMN IF EXISTS requires;
ALTER TABLE releases DROP COLUMN IF EXISTS dependencies;

-- A release without artifacts has no url to go back to, and url is NOT NULL
-- and non-empty in the old shape.
DELETE FROM releases;

ALTER TABLE releases ADD COLUMN IF NOT EXISTS url text NOT NULL DEFAULT '';
ALTER TABLE releases ALTER COLUMN url DROP DEFAULT;

ALTER TABLE releases DROP CONSTRAINT IF EXISTS releases_url_not_empty;
ALTER TABLE releases ADD CONSTRAINT releases_url_not_empty CHECK (url <> '');
