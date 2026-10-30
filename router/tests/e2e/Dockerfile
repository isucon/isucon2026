# syntax=mirror.gcr.io/docker/dockerfile:1.10

FROM mirror.gcr.io/library/golang:1.26.5-alpine AS build

WORKDIR /src
COPY gen/go/ ./gen/go/
COPY router/go.* ./router/
WORKDIR /src/router
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY router/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/router-e2e ./tests/e2e

FROM mirror.gcr.io/library/alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S e2e \
    && adduser -S -G e2e -H -h /nonexistent e2e
COPY --from=build /out/router-e2e /usr/local/bin/router-e2e
USER e2e:e2e
ENTRYPOINT ["/usr/local/bin/router-e2e"]
