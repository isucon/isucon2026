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

FROM alpine:3.23 AS router

RUN apk add --no-cache ca-certificates iproute2 nftables tzdata

COPY --from=build /out/router /usr/local/bin/isureal-router

EXPOSE 18081

HEALTHCHECK --interval=2s --timeout=2s --retries=30 \
  CMD wget -q -O /dev/null http://127.0.0.1:18081/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/isureal-router"]
