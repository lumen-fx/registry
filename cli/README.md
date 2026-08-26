# lpm

Command-line client for the LPM package registry.

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

## Usage

```sh
lpm --version
lpm login                  # paste an API token from the registry's account page
lpm whoami                 # which account the saved token belongs to
lpm publish lantern --platform lumen --description "a lamp"
lpm release lantern 1.0.0 --url https://example.com/lantern-1.0.0.tgz
lpm logout
```

Mint the token in the registry's web UI: sign in with GitHub, open Account,
create a token, and paste it into `lpm login`. The token is saved in your
user config directory (`LPM_CONFIG_DIR` overrides it), readable only by you.

## Development

```sh
go build ./...
go test -race ./...
gofmt -l .
```

## License

See [LICENSE](LICENSE).
