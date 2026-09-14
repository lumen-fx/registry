-- A release carries one artifact per target, plus the requirements a client
-- resolves before it downloads any of them. The registry stores no bytes: an
-- artifact is a URL the publisher hosts, with the digest and size a client
-- checks the download against.

-- The `any` artifact replaces the single url a release used to carry, so
-- every existing row is unrepresentable in the new shape. The table holds
-- only fixture rows, so they go rather than get converted.
DELETE FROM releases;

ALTER TABLE releases DROP CONSTRAINT IF EXISTS releases_url_not_empty;
ALTER TABLE releases DROP COLUMN IF EXISTS url;

-- name -> version requirement for dependencies, host -> version requirement
-- for requires. Empty objects, not null, so a reader never branches on null.
ALTER TABLE releases ADD COLUMN IF NOT EXISTS dependencies jsonb NOT NULL DEFAULT '{}';
ALTER TABLE releases ADD COLUMN IF NOT EXISTS requires jsonb NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS artifacts (
    id         uuid   PRIMARY KEY DEFAULT uuid_generate_v4(),
    release_id uuid   NOT NULL
                      REFERENCES releases (id)
                      ON DELETE CASCADE
                      ON UPDATE CASCADE,
    target     text   NOT NULL,
    url        text   NOT NULL,
    sha256     text   NOT NULL,
    size       bigint NOT NULL,

    -- One artifact per target, so a client picking by target never has to
    -- choose between two rows.
    CONSTRAINT artifacts_release_id_target_key UNIQUE (release_id, target),

    CONSTRAINT artifacts_target_known CHECK (target IN (
        'linux-x86_64',
        'linux-aarch64',
        'macos-x86_64',
        'macos-aarch64',
        'windows-x86_64',
        'windows-aarch64',
        'any'
    )),

    -- The API already requires these. The database says so too, so another
    -- writer cannot store an artifact no client can verify or fetch.
    CONSTRAINT artifacts_sha256_hex CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT artifacts_url_https CHECK (url LIKE 'https://%'),
    CONSTRAINT artifacts_size_positive CHECK (size > 0)
);

CREATE INDEX IF NOT EXISTS artifacts_release_id_idx
    ON artifacts (release_id);
