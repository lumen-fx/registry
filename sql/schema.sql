CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS users (
    id            uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    username      text        NOT NULL,
    email         text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_username_key UNIQUE (username),
    CONSTRAINT users_email_key    UNIQUE (email)
);


CREATE TABLE IF NOT EXISTS packages (
    id           uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    publisher_id uuid        NOT NULL
                             REFERENCES users (id)
                             ON DELETE RESTRICT
                             ON UPDATE CASCADE,
    platform     text        NOT NULL,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT packages_platform_name_key UNIQUE (platform, name),

    CONSTRAINT packages_platform_not_empty CHECK (platform <> ''),
    CONSTRAINT packages_name_not_empty     CHECK (name <> '')
);

CREATE INDEX IF NOT EXISTS packages_name_idx
    ON packages (name);

CREATE INDEX IF NOT EXISTS packages_publisher_id_idx
    ON packages (publisher_id);

CREATE INDEX IF NOT EXISTS packages_created_at_idx
    ON packages (created_at DESC);

CREATE TABLE IF NOT EXISTS releases (
    id          uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    package_id  uuid        NOT NULL
                            REFERENCES packages (id)
                            ON DELETE CASCADE
                            ON UPDATE CASCADE,
    url         text        NOT NULL,
    version     text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT releases_package_id_version_key UNIQUE (package_id, version),

    CONSTRAINT releases_version_not_empty CHECK (version <> '')
);

CREATE INDEX IF NOT EXISTS releases_package_id_created_at_idx
    ON releases (package_id, created_at DESC);

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS packages_name_trgm_idx
    ON packages USING gin (name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS packages_description_trgm_idx
    ON packages USING gin (description gin_trgm_ops);

