# syntax=docker/dockerfile:1.10

FROM golang:1.26.5-alpine AS build

WORKDIR /src

COPY gen/go/ ./gen/go/
COPY router/go.* ./router/
WORKDIR /src/router
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY router/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/router ./cmd/router

# HAProxy runs next to the Router in its network namespace. It renders its
# configuration from the same environment as the Router when it starts.
FROM haproxy:2.8.16-alpine AS haproxy

USER root
RUN install -d -o haproxy -g haproxy /run/isureal-haproxy
COPY --from=build /out/router /usr/local/bin/isureal-router
USER haproxy

ENTRYPOINT ["/bin/sh", "-c", "isureal-router haproxy-config && exec haproxy -W -db -f /run/isureal-haproxy/haproxy.cfg"]

# isureal-digest answers the benchmark's image and export downloads with their
# digests (router/digest). The binary is static (musl), so the same file runs in
# the container and on the Router AMI.
FROM rust:1.95.0-alpine3.23 AS digest-build

WORKDIR /src
COPY router/digest/ ./
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/src/target \
    cargo build --release --locked && install -D target/release/isureal-digest /out/isureal-digest

# The AMI build exports the binary with --target digest-artifact --output.
FROM scratch AS digest-artifact
COPY --from=digest-build /out/isureal-digest /isureal-digest

# It runs next to HAProxy in the Router's network namespace.
FROM alpine:3.23 AS digest
COPY --from=digest-build /out/isureal-digest /usr/local/bin/isureal-digest
USER nobody
ENTRYPOINT ["/usr/local/bin/isureal-digest"]

FROM alpine:3.23 AS router

RUN apk add --no-cache ca-certificates iproute2 nftables tzdata

COPY --from=build /out/router /usr/local/bin/isureal-router

EXPOSE 18081

HEALTHCHECK --interval=2s --timeout=2s --retries=30 \
  CMD wget -q -O /dev/null http://127.0.0.1:18081/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/isureal-router"]
