# ポータル（Go）の本番イメージ。ビルドコンテキストは portal/ ディレクトリ。
#   docker build -f deploy/Dockerfile --build-arg REVISION=$(git rev-parse --short HEAD) -t isuxportal .
# 1 イメージに portal / worker / bot / notifyloop の 5 バイナリ（migrate を含む）と SPA アセットを同梱し、
# CMD でどのサービスを起動するか選ぶ（docker-entrypoint.sh）。

# --- React SPA（webpack 5 + pnpm 9）→ public/packs + manifest.json + sw.js（CT-3 / CT-4）
FROM node:24-alpine AS frontend
WORKDIR /src/frontend
RUN corepack enable && corepack prepare pnpm@9.15.0 --activate
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
ENV NODE_ENV=production
RUN pnpm run webpack

# --- Go バイナリ 5 本（CGO 無効の静的リンク）
FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_BUILD_PROCS=1
ARG GO_BUILD_MEMORY=1GiB
RUN CGO_ENABLED=0 GOMEMLIMIT=${GO_BUILD_MEMORY} GOGC=50 go build -p ${GO_BUILD_PROCS} -trimpath -ldflags='-s -w' -o /out/ ./cmd/portal ./cmd/worker ./cmd/bot ./cmd/notifyloop ./cmd/migrate

# --- 実行イメージ
FROM debian:trixie-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --uid 1000 --create-home --home-dir /opt/portal portal
# RDS certificates are separate from the operating system trust store.
RUN curl --fail --silent --show-error https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem \
    -o /etc/ssl/certs/rds-global-bundle.pem
WORKDIR /opt/portal
COPY --from=build /out/ /opt/portal/bin/
COPY --from=frontend /src/frontend/public/ /opt/portal/public/
COPY --chmod=755 deploy/docker-entrypoint.sh /opt/portal/bin/entrypoint

# config.readRevision はカレントディレクトリの REVISION を読む（/site/sha で返す）
ARG REVISION=unknown
RUN echo "${REVISION}" > /opt/portal/REVISION

ENV ISUXPORTAL_PUBLIC_DIR=/opt/portal/public \
    ISUXPORTAL_PACKS_MANIFEST=/opt/portal/public/packs/manifest.json \
    PORTAL_LISTEN=:8081

VOLUME ["/tmp"]
USER portal
EXPOSE 8081
ENTRYPOINT ["/opt/portal/bin/entrypoint"]
CMD ["portal"]
