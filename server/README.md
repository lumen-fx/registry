# lpm-server

A package registry API. Users register, publish packages, and publish releases
against them. Postgres holds everything; the service is a single Go binary with
no runtime dependencies.

## API

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| `GET` | `/` | none | Liveness. No database access. |
| `GET` | `/health` | none | Pings the pool. `503` when the database is down. |
| `POST` | `/user/register` | none | `201`, or `409` when the username or email is taken. |
| `POST` | `/user/login` | none | Returns the caller's own record with their packages. |
| `POST` | `/user/change_password` | body | `401` on a wrong current password. |
| `GET` | `/users/{username}` | none | Public profile with packages and releases. |
| `GET` | `/users/{username}/packages` | none | That user's packages. |
| `GET` | `/packages` | none | Search. See the filters below. |
| `POST` | `/packages` | basic | `201`, or `409` when the name is taken. |
| `GET` | `/packages/{package}` | none | One package with its releases, newest first. |
| `GET` | `/packages/{package}/releases` | none | Just the releases. |
| `POST` | `/packages/{package}/releases` | basic | Publisher only. `403` for anyone else. |
| `GET` | `/packages/{package}/releases/{version}` | none | One release. |

Search filters on `GET /packages`, all optional and combined with AND:
`platform`, `name`, `q` (name or description), `username`, `version`, `limit`.
No filter lists the newest packages. `limit` defaults to 50 and is capped at
200. An unparseable `limit` is a `422`, not a silent fallback.

Errors are JSON. Validation failures return `422` with a `fields` object, so a
client can fix a whole form from one response:

```json
{
  "error": "request contains invalid fields",
  "fields": {"url": "must use https", "version": "is required"}
}
```

Release URLs must be `https` with no embedded credentials. Clients fetch them
to install code, so plain `http` would leave the artifact open to tampering in
transit.

## Layout

```
main.go            process lifecycle
cmd/migrate/       standalone migrator, run as a Kubernetes Job
migrations/        the schema, embedded in both binaries
src/               server, handlers, store, validation, middleware
scripts/           schema dump helper
```

## Running it

Requires Go 1.26 and a Postgres. `DATABASE_URL` is the only required setting.

```sh
docker run -d --name lpm-pg -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=lpm postgres:18

export DATABASE_URL='postgres://postgres:postgres@localhost:5432/lpm'
go run ./cmd/migrate
go run .
```

`.env` is read if present, so the exports can live there instead. The server
listens on `:8080`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | none, required | Postgres connection string. |
| `MIGRATE_ON_BOOT` | `false` | Migrate before serving. Local convenience only. |
| `TEST_DATABASE_URL` | none | Database for the end-to-end tests. |

Leave `MIGRATE_ON_BOOT` off in a cluster. Every replica would migrate at once
during a rollout; the Job in `k8s/base` owns the schema instead.

## Migrations

`migrations/` holds `NNNNNN_name.up.sql` and `.down.sql` pairs, embedded in the
binary so it carries its own schema history. `golang-migrate` tracks what has
been applied in a `schema_migrations` table.

```sh
go run ./cmd/migrate                    # apply everything outstanding
scripts/dump-schema.sh                  # print the schema of a migrated database
```

Applying twice is a no-op. To add a change, write the next numbered pair and
leave the existing files alone: editing an applied migration changes nothing on
a database that already ran it, which is how a schema and its history drift
apart.

A database created before migrations existed needs its version recorded once,
otherwise the first run tries to create tables that are already there:

```sh
go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
  -path migrations -database "pgx5://..." force 5
```

Pick the number matching what that database already has, then migrate normally.

## Tests

```sh
go test ./...                                     # unit and failure paths
TEST_DATABASE_URL='postgres://...' go test ./...  # adds the end-to-end suite
```

The end-to-end tests run migrations, then drive the real router over real HTTP
against a real Postgres, through the same middleware chain `NewHTTPServer`
builds. Without `TEST_DATABASE_URL` they skip themselves, so a run that reports
success while testing almost nothing is possible: CI always sets it.

They truncate between tests. Point `TEST_DATABASE_URL` at a database you do not
mind emptying.

CI runs `gofmt`, `go vet`, and the suite under `-race` against a `postgres:18`
service. Coverage goes to Codecov, where the patch status is a required check
at 80 percent of changed lines.

Deployment (the image, the manifests in `k8s/`, and the rollout) is covered in
the [repository README](../README.md).
