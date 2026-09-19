# lpm-server

A package registry API. Users sign in with GitHub, publish packages, and
publish releases against them. Postgres holds everything; the service is a
single Go binary with no runtime dependencies.

## API

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| `GET` | `/` | none | The web UI. Static, no database access, doubles as liveness. |
| `GET` | `/health` | none | Pings the pool. `503` when the database is down. |
| `GET` | `/install.sh` | none | Installer for the `lpm` CLI. Static. |
| `GET` | `/assets/{asset}` | none | The scripts the UI renders README markdown with. Versioned, cached for good. |
| `GET` | `/auth/github/login` | none | Starts GitHub sign-in with a redirect. |
| `GET` | `/auth/github/callback` | none | Finishes sign-in and sets the session cookie. |
| `POST` | `/auth/logout` | session | Ends the browser session. |
| `GET` | `/auth/me` | session or token | The signed-in account. |
| `GET` | `/tokens` | session | The account's API tokens, without their secrets. |
| `POST` | `/tokens` | session | Mints a token. The secret appears once, in this response. |
| `DELETE` | `/tokens/{token}` | session | Revokes a token. |
| `GET` | `/users/{username}` | none | Public profile with packages and releases. |
| `GET` | `/users/{username}/packages` | none | That user's packages. |
| `GET` | `/packages` | none | Search. See the filters below. |
| `POST` | `/packages` | token | `201`, or `409` when the name is taken. |
| `GET` | `/packages/{package}` | none | One package with its releases, newest first. |
| `DELETE` | `/packages/{package}` | session or token | Publisher only. `204`, or `409` when it has releases. |
| `GET` | `/packages/{package}/readme` | none | The newest release's README. `404` when there is none. |
| `GET` | `/packages/{package}/downloads` | none | Downloads of the package's archives, total and per day. |
| `GET` | `/packages/{package}/releases` | none | Just the releases. |
| `POST` | `/packages/{package}/releases` | token | Publisher only. `403` for anyone else. |
| `GET` | `/packages/{package}/releases/{version}` | none | One release. |

Search filters on `GET /packages`, all optional and combined with AND:
`platform`, `name`, `q` (name or description), `username`, `version`, `limit`.
No filter lists the newest packages. `limit` defaults to 50 and is capped at
200. An unparseable `limit` is a `422`, not a silent fallback.

## Packages, releases, and artifacts

A package has a globally unique name and belongs to one platform, either
`lumen` or `candela`. `POST /packages` takes `platform`, `name`, and
`description`, and answers `201`. The publisher who holds a name posts it again
to change the description and gets `200` with the package; anyone else gets
`409`. The platform is fixed by the first claim.

`DELETE /packages/{package}` frees a name its publisher claimed and never
released to, so a name taken by a mistaken run does not stay taken. A package
with releases answers `409`: clients resolve against it, and a freed name is a
name someone else can claim.

A release carries the version, what it depends on, what it needs from its
host, and one artifact per target. The registry stores no bytes: the publisher
hosts the archives, and the registry records where each one is, what it hashes
to, and how big it is, so a client can verify what it downloaded.

`POST /packages/{package}/releases`:

```json
{
  "version": "1.2.3",
  "description": "faster tessellation",
  "dependencies": {"geom": "^0.3"},
  "requires": {"lumenc": ">=0.2, <1"},
  "artifacts": [
    {
      "target": "linux-x86_64",
      "url": "https://github.com/you/shape-tools/releases/download/v1.2.3/linux-x86_64.tar.gz",
      "sha256": "0123...",
      "size": 481922
    }
  ]
}
```

`version` is semver: `MAJOR.MINOR.PATCH`, with an optional pre-release and
build metadata. `dependencies` maps a package name to a version requirement
and `requires` maps a host name to one. Both default to `{}`.

A requirement is read with the same grammar `lpm` resolves it by, from the
`cli/req` package the server imports, so a release the registry accepts is one
a client can resolve. `^1.2`, `~1.2`, `=1.2.3`, `>=1, <2`, `1.2.*`, and `*` all
mean what cargo means by them, and a bare `1.2` is a caret requirement. A
requirement that does not parse is a `422` naming its field, such as
`dependencies.geom`.

Every release needs at least one artifact. `target` is one of `linux-x86_64`,
`linux-aarch64`, `macos-x86_64`, `macos-aarch64`, `windows-x86_64`,
`windows-aarch64`, or `any`, and a release names each target at most once. A
client prefers the artifact for its own target and falls back to `any`.
`sha256` is 64 lowercase hex characters and `size` is the archive's size in
bytes.

Artifact URLs must be `https` with no embedded credentials. Clients fetch them
to install code, so plain `http` would leave the archive open to tampering in
transit.

Every `GET` that returns a release returns it in the same shape, plus `id` and
`createdAt`, with the artifacts ordered by target.

Errors are JSON. Validation failures return `422` with a `fields` object, so a
client can fix a whole form from one response:

```json
{
  "error": "request contains invalid fields",
  "fields": {
    "artifacts[0].url": "must use https",
    "version": "is required"
  }
}
```

A release and its artifacts are written together, so a `422` or a conflict
leaves the version free for the next attempt.

## READMEs and downloads

The registry stores no bytes, so it holds neither the documentation for a
package nor a count of its downloads. Both come from the publisher's GitHub
release and are cached here, so a page view does not depend on GitHub
answering.

The repository is read off the artifacts: a release archive at
`https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>` names the
repository and the tag. A package whose archives are hosted anywhere else has
no README and no download figures here, and there is no field to declare one.

`GET /packages/{package}/readme` answers with the newest release's README as it
stood at that tag, as markdown. The browser renders it. A fetch that fails
leaves the copy from before it, marked `stale`, rather than blanking the page;
a README the publisher deletes stops being served. Nothing to show is a `404`.

`GET /packages/{package}/downloads` answers with `total`, the newest reading of
every archive the package has ever released, and `days`, one entry per day.
GitHub reports a running total per asset and keeps no history, so the daily
figures are the rise between two samples: `/collect` takes one a day, the first
sample of an archive draws no bar, and a day the collector does not run leaves a
gap the next run absorbs.

The figures count downloads of the archives on GitHub. A browser, a mirror, or
a CI job fetching one is in the number, so it is not an install count, and the
UI says so.

The browser renders the markdown with two libraries, `marked` and `DOMPurify`.
A README is written by whoever published the package, so it is sanitised before
it reaches the page. They are the only third-party code the UI runs, and they
are served from the binary rather than a CDN, so a page load reaches no third
party.

Their bytes are not in the repository. `scripts/fetch-web-assets.sh` downloads
the pinned versions, refuses anything whose sha256 does not match, and leaves
them in `web/assets` for the embed to pick up; the digests are of what npm
published. Run it before building or testing the server, or the build stops on
an embed with nothing to embed. A rebuild with the files already there fetches
nothing. To move a version, change the pin, the digest, and the file name in
the same edit: the name carries the version, and the page asks for it by name.

## Accounts

Accounts come from GitHub sign-in; there are no passwords. The OAuth callback
sets a week-long session cookie for the browser, and publishing uses API
tokens minted on the UI's account page. A token is sent as
`Authorization: Bearer lpm_...` and is stored hashed, so its secret exists
only in the create response. Set `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`
from a GitHub OAuth app whose callback is `<host>/auth/github/callback`;
without them, sign-in answers `503` and the rest of the API works read-only.

## Layout

```
main.go            process lifecycle
cmd/migrate/       standalone migrator, run as a Kubernetes Job
cmd/collect/       daily GitHub sample, run as a Kubernetes CronJob
migrations/        the schema, embedded in every binary
src/               server, handlers, store, validation, middleware
web/               browser UI, CLI installer, and the UI's scripts, embedded
scripts/           schema dump and web asset fetch helpers
```

## Running it

Requires Go 1.26 and a Postgres. `DATABASE_URL` is the only required setting.

```sh
docker run -d --name lpm-pg -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=lpm postgres:18

scripts/fetch-web-assets.sh
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/lpm'
go run ./cmd/migrate
go run .
```

The first line fetches the UI's markdown libraries, which are not committed.
Every build needs them once; the Dockerfile and CI run the same script.

`.env` is read if present, so the exports can live there instead. The server
listens on `:8080`.

```sh
go run ./cmd/collect
```

reads GitHub once for every release in the registry and exits. Without it a
package page shows no README and no downloads, because nothing has been
sampled yet.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | none, required | Postgres connection string. |
| `GITHUB_CLIENT_ID` | none | OAuth app client id. Sign-in is `503` without it. |
| `GITHUB_CLIENT_SECRET` | none | OAuth app client secret. |
| `GITHUB_TOKEN` | none | Read-only token for the README and download calls. Sixty calls an hour without one. |
| `GITHUB_API_URL` | `https://api.github.com` | Where those calls go. The tests point it at a stand-in. |
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
scripts/fetch-web-assets.sh                       # once per checkout
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
