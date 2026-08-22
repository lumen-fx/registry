CREATE TABLE IF NOT EXISTS users (
    id            uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    username      text        NOT NULL,
    email         text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_username_key UNIQUE (username),
    CONSTRAINT users_email_key    UNIQUE (email)
);