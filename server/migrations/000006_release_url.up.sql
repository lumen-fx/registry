ALTER TABLE releases ADD COLUMN IF NOT EXISTS url text NOT NULL DEFAULT '';
ALTER TABLE releases ALTER COLUMN url DROP DEFAULT;

-- The API already requires a url. The database says so too, so another writer
-- cannot store a release nobody can download.
ALTER TABLE releases DROP CONSTRAINT IF EXISTS releases_url_not_empty;
ALTER TABLE releases ADD CONSTRAINT releases_url_not_empty CHECK (url <> '');
