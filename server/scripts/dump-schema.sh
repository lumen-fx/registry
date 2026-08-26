#!/usr/bin/env bash
# Prints the schema of a migrated database, for reading or for diffing two
# environments. Nothing commits the output: the migrations in migrations/ are
# the source of truth, and a committed dump would drift from them.
set -euo pipefail

DATABASE_URL="${DATABASE_URL:?set DATABASE_URL to a migrated database}"

pg_dump "$DATABASE_URL" --schema-only --no-owner --no-privileges \
  --exclude-table=schema_migrations
