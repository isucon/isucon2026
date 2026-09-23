#!/usr/bin/env bash
# Go サーバをテストモードで起動し、Docker 上の Ruby から HTTP 化 RSpec を実行する。
# 環境変数は Makefile の GO_TEST_ENV で与えられる前提（単体でも動くよう既定値を持つ）。
set -euo pipefail
cd "$(dirname "$0")/.."

export PORTAL_TEST_MODE=${PORTAL_TEST_MODE:-1}
export PORTAL_AUTO_MIGRATE=${PORTAL_AUTO_MIGRATE:-1}
export PORTAL_LISTEN=${PORTAL_LISTEN:-:8081}
export ISUXPORTAL_DATABASE_URL=${ISUXPORTAL_DATABASE_URL:-postgres://isuxportal:isuxportal@127.0.0.1:15432/isuxportal?sslmode=disable}
export REDIS_URL=${REDIS_URL:-redis://127.0.0.1:16379/0}

go build -o bin/portal ./cmd/portal
./bin/portal &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT

for _ in $(seq 1 50); do
  if curl -sf http://127.0.0.1:8081/site/sha >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

docker run --rm \
  -v "$PWD/rspec:/work" \
  -e TARGET_BASE=http://host.docker.internal:8081 \
  -e DB_HOST=host.docker.internal \
  -e DB_PORT=15432 \
  isuxportal-rspec \
  bash -c 'bundle check >/dev/null 2>&1 || bundle install --quiet; bundle exec rspec "$@"' _ "$@"
