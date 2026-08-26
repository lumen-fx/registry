ALTER TABLE packages DROP CONSTRAINT IF EXISTS packages_platform_name_key;

-- A UNIQUE constraint builds its own index, so a plain one on name is dead
-- weight on every insert and update.
DROP INDEX IF EXISTS packages_name_idx;

ALTER TABLE packages DROP CONSTRAINT IF EXISTS packages_name_key;
ALTER TABLE packages ADD CONSTRAINT packages_name_key UNIQUE (name);
