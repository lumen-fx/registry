-- GitHub accounts cannot be mapped back to password accounts, so the rows
-- created under this schema are removed the same way the up migration
-- removed the password ones.
DELETE FROM releases;
DELETE FROM packages;
DELETE FROM users;

DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS sessions;

ALTER TABLE users
    DROP CONSTRAINT users_github_id_key,
    DROP COLUMN github_id,
    ADD COLUMN email text NOT NULL,
    ADD COLUMN password_hash text NOT NULL,
    ADD CONSTRAINT users_email_key UNIQUE (email);
