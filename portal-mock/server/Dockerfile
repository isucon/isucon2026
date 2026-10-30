# syntax=mirror.gcr.io/docker/dockerfile:1.10

FROM mirror.gcr.io/library/golang:1.26.5-alpine AS build

WORKDIR /src

# 共有生成コードは portal-mock/server の外にあるため、build context はリポジトリルート。
COPY gen/go/ ./gen/go/
COPY portal-mock/server/go.* ./portal-mock/server/
WORKDIR /src/portal-mock/server
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY portal-mock/server/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/portal ./cmd/portal

FROM mirror.gcr.io/library/alpine:3.23

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S portal \
    && adduser -S -G portal -H -h /nonexistent portal

COPY --from=build /out/portal /usr/local/bin/portal

USER portal:portal
EXPOSE 8080

ENV PORTAL_LISTEN_ADDRESS=0.0.0.0:8080

HEALTHCHECK --interval=5s --timeout=2s --retries=12 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/portal"]
