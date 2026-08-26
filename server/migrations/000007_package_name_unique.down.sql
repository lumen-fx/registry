ALTER TABLE packages DROP CONSTRAINT IF EXISTS packages_name_key;

CREATE INDEX IF NOT EXISTS packages_name_idx
    ON packages (name);

ALTER TABLE packages DROP CONSTRAINT IF EXISTS packages_platform_name_key;
ALTER TABLE packages ADD CONSTRAINT packages_platform_name_key UNIQUE (platform, name);
