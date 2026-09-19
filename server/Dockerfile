# Build the three binaries, then ship them on a base with no shell and no
# package manager. The server, the migrator, and the collector share one image,
# so a rollout, its migration Job, and the daily sample always run the same
# code.
FROM golang:1.26 AS build

# The build context is the repository root: server/go.mod replaces the cli
# module with ../cli, so the module graph does not resolve from server/ alone.
WORKDIR /src
COPY cli/go.mod cli/go.sum cli/
COPY server/go.mod server/go.sum server/

WORKDIR /src/server
RUN go mod download

COPY cli /src/cli
COPY server /src/server

# The libraries the UI renders README markdown with are not in the repository.
# The build fetches them at their pinned versions, checks their digests, and
# the server embeds what it fetched.
RUN scripts/fetch-web-assets.sh

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/lpm-server . \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags "-s -w" \
        -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags "-s -w" \
        -o /out/collect ./cmd/collect

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/lpm-server /lpm-server
COPY --from=build /out/migrate /migrate
COPY --from=build /out/collect /collect

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/lpm-server"]
