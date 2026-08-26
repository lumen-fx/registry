# lpm

Command-line client for the LPM package registry.

## Install

```sh
curl -fsSL https://registry.lumenfx.dev/install.sh | sh
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
```

## Development

```sh
go build ./...
go test -race ./...
gofmt -l .
```

## License

See [LICENSE](LICENSE).
