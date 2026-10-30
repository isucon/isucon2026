FROM mirror.gcr.io/library/node:24-alpine AS frontend
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@9.15.0 --activate
COPY portal/frontend/package.json portal/frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY portal/frontend/ ./
RUN NODE_ENV=production pnpm run webpack

FROM mirror.gcr.io/library/golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
WORKDIR /src
COPY portal/ ./
RUN CGO_ENABLED=0 go build -p 2 -o /out/ ./cmd/portal ./cmd/worker ./cmd/local-debug

FROM mirror.gcr.io/library/debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home portal
WORKDIR /opt/portal
COPY --from=build /out/ ./bin/
COPY --from=frontend /src/public/ ./public/
ENV ISUXPORTAL_PUBLIC_DIR=/opt/portal/public \
    ISUXPORTAL_PACKS_MANIFEST=/opt/portal/public/packs/manifest.json
USER portal
CMD ["/opt/portal/bin/portal"]
