CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS packages_name_trgm_idx
    ON packages USING gin (name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS packages_description_trgm_idx
    ON packages USING gin (description gin_trgm_ops);
