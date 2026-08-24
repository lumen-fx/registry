-- RESTRICT keeps published packages from being orphaned.

CREATE TABLE IF NOT EXISTS packages (
    id           uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    publisher_id uuid        NOT NULL
                             REFERENCES users (id)
                             ON DELETE RESTRICT
                             ON UPDATE CASCADE,
    platform     text        NOT NULL,
    name         text        NOT NULL UNIQUE,
    description  text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT packages_platform_not_empty CHECK (platform <> ''),
    CONSTRAINT packages_name_not_empty     CHECK (name <> '')
);

CREATE INDEX IF NOT EXISTS packages_name_idx
    ON packages (name);

CREATE INDEX IF NOT EXISTS packages_publisher_id_idx
    ON packages (publisher_id);

CREATE INDEX IF NOT EXISTS packages_created_at_idx
    ON packages (created_at DESC);
