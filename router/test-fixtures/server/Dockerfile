# syntax=docker/dockerfile:1.10

FROM golang:1.26.5-alpine AS build

WORKDIR /src
COPY gen/go/ ./gen/go/
COPY router/go.* ./router/
WORKDIR /src/router
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY router/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/router-test-server ./test-fixtures/server

FROM alpine:3.23
RUN apk add --no-cache ca-certificates iproute2 tzdata
COPY --from=build /out/router-test-server /usr/local/bin/router-test-server
COPY router/test-fixtures/server/entrypoint.sh /usr/local/bin/router-test-entrypoint
RUN chmod 0755 /usr/local/bin/router-test-entrypoint

EXPOSE 8080
HEALTHCHECK --interval=2s --timeout=2s --retries=30 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/router-test-entrypoint"]
