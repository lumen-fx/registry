# lpm

Command-line client for the LPM package registry. It publishes packages, and it
installs them: resolving requirements to one version each, verifying every
download against the digest the registry published, and recording the result in
a lock file.

`lumenc` and the `candela` CLI use lpm as their package manager. They shell out
to it, pass their requirements on the command line, and read its JSON. One copy
serves both, at `~/.local/bin/lpm`, or `%LOCALAPPDATA%\Programs\lpm\lpm.exe` on
Windows.

## Install

```sh
curl -fsSL https://reg.lumenfx.dev/install.sh | sh
```

Or with Go:

```sh
go install github.com/lumen-fx/registry/cli@latest
```

Or download a binary for your platform from the
[releases page](https://github.com/lumen-fx/registry/releases) and put it on your
`PATH`.

## Using it

```sh
lpm search shapes                  # find a package
lpm info shape-tools               # its releases and what each one needs

lpm install --lock lumen.lock --target linux-x86_64 \
    --host lumenc@0.4.0 --req shape-tools@^1.2

lpm update shape-tools --lock lumen.lock --target linux-x86_64 \
    --host lumenc@0.4.0 --req shape-tools@^1.2
```

Publishing needs an account. Sign in to the registry's web UI with GitHub, open
Account, create a token, and paste it into `lpm login`. The token is saved in
your user config directory, readable only by you.

A CI job has no terminal to paste into: give it the token as `LPM_TOKEN` and
`publish`, `release` and `whoami` use it without a login.

```sh
lpm login
lpm whoami
lpm publish shape-tools --platform lumen --description "tessellation helpers"
lpm release shape-tools 1.2.3 \
    --artifact linux-x86_64=https://github.com/you/shape-tools/releases/download/v1.2.3/linux-x86_64.tar.gz \
    --artifact macos-aarch64=https://github.com/you/shape-tools/releases/download/v1.2.3/macos-aarch64.tar.gz \
    --dep geom@^0.3 --requires 'lumenc@>=0.2' \
    -d "faster tessellation"
lpm logout
```

## Commands

### install

```
lpm install --lock PATH --target TARGET [--host NAME@VERSION]... [--req NAME@REQUIREMENT]...
            [--locked] [--offline] [--registry URL] [--json]
```

Resolves every requirement, downloads what is missing, and writes the lock when
it changed.

Resolution settles one version per package name: the highest release that
satisfies every requirement on it, whose `requires` are all met by the `--host`
versions you passed, and which publishes an artifact for `TARGET` or for `any`.
It then follows that release's own dependencies and repeats. A `requires`
naming a host you did not pass is unmet.

A version already pinned in the lock wins while it still satisfies everything,
so installing one package does not move the others. When two packages want
versions of a third that cannot both be had, the error names both of them and
what each asked for.

`--target` is the machine to install for, so `any` is not a value it takes.
Requirements never come from a project file: the host CLI reads its own
manifest and passes what it found.

| Flag | Meaning |
| --- | --- |
| `--lock PATH` | The lock file to read pins from and write them to. Required. |
| `--target TARGET` | The machine to install for. Required. |
| `--host NAME@VERSION` | A host and the version of it you have. Repeatable. |
| `--req NAME@REQUIREMENT` | A package and what is wanted of it. Repeatable. |
| `--locked` | Exit 3 rather than write a lock that would change. |
| `--offline` | Resolve from the lock and install from the cache, reaching no network. Exit 4 on anything neither holds. |
| `--registry URL` | Which registry to resolve against. |
| `--json` | Report what was installed as JSON instead of one line per package. |

### update

```
lpm update [NAME...] --lock PATH --target TARGET [the same flags as install]
```

The same run as `install`, except that the named packages are free to move:
their pins are ignored and the newest satisfying release wins. Name nothing and
every pin is reconsidered. Anything the named packages do not reach stays where
the lock put it.

### search and info

```
lpm search QUERY [--platform PLATFORM] [--registry URL]
lpm info NAME [--registry URL]
```

`search` matches a name or a description, and `--platform` narrows it to
`lumen` or `candela`. `info` prints one package with every release, the targets
each one publishes, and what each depends on and needs.

### publish and release

```
lpm publish NAME --platform PLATFORM [-d DESCRIPTION] [--registry URL]
lpm release NAME VERSION --artifact TARGET=URL... [--dep NAME@REQUIREMENT]...
            [--requires HOST@REQUIREMENT]... [-d DESCRIPTION] [--registry URL]
```

`publish` claims a name; names are unique across the whole registry. `release`
adds one version to a package you own.

You host the archives and the registry records where they are. `release`
downloads every artifact, hashes it, and publishes the digest and the size with
the URL, so what the registry records is what the URL served. Each URL must be
`https`.

A version is semver: `MAJOR.MINOR.PATCH`, with an optional pre-release and
build metadata.

### login, logout, and whoami

`login` reads an API token and saves it. `whoami` reports which account it
belongs to. `logout` forgets the saved one, and says so when `LPM_TOKEN` is
still set, because that token keeps signing you in.

`LPM_TOKEN` authenticates every command that needs a token, so a CI job that
holds the secret never runs `login`. A token saved by `login` outranks it.
`whoami` takes `--registry` like the publishing commands do.

## Version requirements

Requirements read the way cargo's do.

| Requirement | Matches |
| --- | --- |
| `^1.2` | `>=1.2.0` and `<2.0.0` |
| `1.2` | the same; a bare requirement is a caret requirement |
| `~1.2` | `>=1.2.0` and `<1.3.0` |
| `=1.2.3` | that version alone |
| `>=1, <2` | both comparators |
| `1.2.*` | any patch of 1.2 |
| `*` | any version |
| `^1 \|\| ^3` | either |

A pre-release is matched only by a requirement that names one.

The registry reads requirements with the same grammar, so it rejects a release
carrying one lpm could not resolve rather than storing it for every client to
fail on.

## Targets

An artifact is built for one target, and `any` is the artifact that runs
everywhere. A package publishes as many as it built.

```
linux-x86_64    linux-aarch64
macos-x86_64    macos-aarch64
windows-x86_64  windows-aarch64
any
```

Installing for a target prefers the artifact for that target and falls back to
`any`.

## Artifact layout

A tarball's root is the package root. An archive whose entries all sit under
one directory has that directory stripped, because `tar czf pkg.tar.gz pkg/`
produces one and it is not part of the package.

- A Lumen module or plugin holds its library file at the root:
  `libshape_tools.so`, `shape_tools.dll`, `libshape_tools.dylib`.
- A candela package holds `candela.toml` and its `.cdl` files at the root. Any
  native libraries it needs sit at the root of the per-target artifact.

Archives are `.tar.gz` or `.zip`. An entry naming a parent directory, and an
entry that is not a regular file or a directory, are both refused.

## The lock file

lpm is the only writer. The host CLI chooses the path and commits the file.

```toml
# Generated by lpm. Commit this file.
version = 2

[[package]]
name = "shape-tools"
version = "1.2.3"
platform = "lumen"
registry = "https://reg.lumenfx.dev"
dependencies = ["geom 0.3.1"]

[package.artifacts]
linux-x86_64 = "sha256:0123..."
any = "sha256:4567..."
```

`dependencies` records the version each dependency resolved to, not the
requirement. `[package.artifacts]` holds the digest of every artifact lpm has
verified; a run on another machine adds its own target and leaves the rest
alone, so one lock covers a team on mixed machines.

Packages are sorted by name and every map is written in key order, so the same
resolution produces the same bytes. A lock whose `version` is not 2 is an error
naming the version it found.

## The cache

An artifact unpacks once per machine, into:

```
<cache>/lpm/pkgs/<name>/<version>/<target>/
```

`<cache>` is the platform's own cache directory, and `LPM_CACHE_DIR` overrides
the whole root.

Each package directory holds the unpacked archive plus a `.lpm` file naming the
digest it was verified against. lpm writes the marker only after a verified
unpack, so a directory without one is what an interrupted run left behind; it
is fetched again rather than trusted.

## JSON output

`install --json` and `update --json` print one document, schema 1:

```json
{
  "schema": 1,
  "lock": "/abs/path/lumen.lock",
  "packages": [
    {
      "name": "shape-tools",
      "version": "1.2.3",
      "platform": "lumen",
      "target": "linux-x86_64",
      "dir": "/home/u/.cache/lpm/pkgs/shape-tools/1.2.3/linux-x86_64",
      "files": ["libshape_tools.so"],
      "dependencies": {"geom": "0.3.1"}
    }
  ]
}
```

`dir` is absolute and `files` lists its top-level entries without the `.lpm`
marker, so a host CLI finds the library file without knowing the packaging
convention. Read `schema` first and refuse a number you do not know.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Done. |
| 1 | Something failed. |
| 2 | A usage mistake: a missing flag, an unknown target, a malformed `NAME@VERSION`. |
| 3 | `--locked` was given and the lock would have changed. |
| 4 | `--offline` was given and the lock or the cache does not hold what was asked for. |

A failure is one line on stderr.

## Environment

| Variable | Meaning |
| --- | --- |
| `LPM_CONFIG_DIR` | Where the saved registry and token live. Defaults to your user config directory. |
| `LPM_CACHE_DIR` | The cache root. Defaults to your user cache directory. |
| `LPM_REGISTRY` | The registry to use when `--registry` is absent and nothing is saved. |
| `LPM_TOKEN` | The API token to publish with when `lpm login` saved none. |
| `LPM_CA_FILE` | A PEM certificate authority to trust in addition to the system ones. |

`--registry` wins, then what `lpm login` saved, then `LPM_REGISTRY`, then
`https://reg.lumenfx.dev`. Every command picks its registry this way, the
publishing ones included.

The token follows the same order without a flag: what `lpm login` saved, then
`LPM_TOKEN`. Surrounding whitespace is trimmed, so a secret that arrives with a
newline on the end still works.

`LPM_CA_FILE` is for a registry or an artifact host behind a private
certificate authority. It adds to the system trust store and never replaces it,
so nothing is trusted without it that was not already.

## Development

```sh
go build ./...
go test -race ./...
gofmt -l .
```

## License

See [LICENSE](LICENSE).
