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

FROM alpine:3.23

RUN apk add --no-cache ca-certificates iproute2 nftables tzdata

COPY --from=build /out/router /usr/local/bin/isureal-router

EXPOSE 18000 18081

HEALTHCHECK --interval=2s --timeout=2s --retries=30 \
  CMD wget -q -O /dev/null http://127.0.0.1:18081/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/isureal-router"]
