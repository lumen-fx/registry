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

    -- A version is published once per package.
    CONSTRAINT releases_package_id_version_key UNIQUE (package_id, version),

    CONSTRAINT releases_version_not_empty CHECK (version <> '')
);

CREATE INDEX IF NOT EXISTS releases_package_id_created_at_idx
    ON releases (package_id, created_at DESC);
