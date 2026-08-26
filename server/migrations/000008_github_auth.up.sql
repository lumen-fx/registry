-- Accounts come from GitHub sign-in. Password accounts cannot be mapped to a
-- GitHub identity, so the rows from before this migration are removed along
-- with everything they published.
DELETE FROM releases;
DELETE FROM packages;
DELETE FROM users;

ALTER TABLE users
    DROP COLUMN email,
    DROP COLUMN password_hash,
    ADD COLUMN github_id bigint NOT NULL,
    ADD CONSTRAINT users_github_id_key UNIQUE (github_id);

-- Browser sessions from the OAuth callback. The id only exists hashed, so a
-- leaked table cannot be replayed as a cookie.
CREATE TABLE IF NOT EXISTS sessions (
    id_hash    text        PRIMARY KEY,
    user_id    uuid        NOT NULL
                           REFERENCES users (id)
                           ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id);

-- API tokens minted in the UI and used by the CLI. Same hashing rule as
-- sessions.
CREATE TABLE IF NOT EXISTS tokens (
    id           uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      uuid        NOT NULL
                             REFERENCES users (id)
                             ON DELETE CASCADE,
    name         text        NOT NULL,
    token_hash   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,

    CONSTRAINT tokens_token_hash_key UNIQUE (token_hash),
    CONSTRAINT tokens_name_not_empty CHECK (name <> '')
);

CREATE INDEX IF NOT EXISTS tokens_user_id_idx ON tokens (user_id);
