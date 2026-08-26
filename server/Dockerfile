# Build both binaries, then ship them on a base with no shell and no package
# manager. The server and the migrator share one image, so a rollout and its
# migration Job always run the same code.
FROM golang:1.26 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/lpm-server . \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags "-s -w" \
        -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/lpm-server /lpm-server
COPY --from=build /out/migrate /migrate

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/lpm-server"]
