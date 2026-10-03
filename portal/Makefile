# ポータル Go rewrite の開発用タスク。
# `make rspec` で PostgreSQL/Redis 起動 → Go サーバ起動 → HTTP 化 RSpec 実行まで一気に行う。

GO_TEST_ENV := PORTAL_TEST_MODE=1 PORTAL_AUTO_MIGRATE=1 PORTAL_LISTEN=:8081 \
	ISUXPORTAL_DATABASE_URL='postgres://isuxportal:isuxportal@127.0.0.1:15432/isuxportal?sslmode=disable' \
	REDIS_URL=redis://127.0.0.1:16379/0 \
	DEFAULT_URL_HOST=127.0.0.1:8081 \
	AWS_ACCESS_KEY_ID=test-dummy AWS_SECRET_ACCESS_KEY=test-dummy \
	ISUXPORTAL_AVATAR_BUCKET=isuxportal-avatars-test \
	ISUXPORTAL_TEST_AMI_ID=ami-test \
	ISUXPORTAL_ADMIN_LOGIN=admin ISUXPORTAL_ADMIN_PASSWORD=adminpass \
	ISUXPORTAL_GITHUB_CLIENT_ID=gh-client ISUXPORTAL_GITHUB_CLIENT_SECRET=gh-secret \
	ISUXPORTAL_GITHUB_AUTHORIZE_URL=http://host.docker.internal:8081/debug/oauth/github/authorize \
	ISUXPORTAL_GITHUB_TOKEN_URL=http://127.0.0.1:8081/debug/oauth/github/token \
	ISUXPORTAL_GITHUB_API_BASE_URL=http://127.0.0.1:8081/debug/oauth/github \
	ISUXPORTAL_DISCORD_CLIENT_ID=dc-client ISUXPORTAL_DISCORD_CLIENT_SECRET=dc-secret \
	ISUXPORTAL_DISCORD_AUTHORIZE_URL=http://host.docker.internal:8081/debug/oauth/discord/authorize \
	ISUXPORTAL_DISCORD_TOKEN_URL=http://127.0.0.1:8081/debug/oauth/discord/token \
	ISUXPORTAL_DISCORD_API_BASE_URL=http://127.0.0.1:8081/debug/oauth/discord

TEST_DB_ENV := ISUXPORTAL_TEST_DATABASE_URL='postgres://isuxportal:isuxportal@127.0.0.1:15432/isuxportal?sslmode=disable' \
	ISUXPORTAL_TEST_REDIS_URL='redis://127.0.0.1:16379/1'

.PHONY: build
build:
	go build ./...

# 単体 + 契約テスト（DB 不要。CI の portal-unit-contract-build 相当）
.PHONY: test
test:
	go test ./internal/... ./tests/contract -count=1

# PostgreSQL + Redis を使う結合テスト（CI の portal-integration-migration 相当）
.PHONY: test-integration
test-integration: db-up
	$(TEST_DB_ENV) go test ./tests/integration -count=1

# 並行時の不変条件（CI の portal-concurrency 相当）
.PHONY: test-concurrency
test-concurrency: db-up
	$(TEST_DB_ENV) go test ./tests/concurrency -count=1

.PHONY: db-up
db-up:
	docker compose up -d --wait postgres redis

.PHONY: db-down
db-down:
	docker compose down -v

# React SPA（isucon14-portal 由来）のビルド。出力は frontend/public/packs + manifest.json（CT-3）
.PHONY: frontend
frontend:
	cd frontend && pnpm install --frozen-lockfile && NODE_ENV=production pnpm run webpack

FRONTEND_ENV := ISUXPORTAL_PUBLIC_DIR=frontend/public \
	ISUXPORTAL_PACKS_MANIFEST=frontend/public/packs/manifest.json

.PHONY: server
server: db-up
	$(GO_TEST_ENV) $(FRONTEND_ENV) go run ./cmd/portal

# 本番イメージ（deploy/Dockerfile）のローカルビルド。push は GitHub Actions（portal-deploy.yml）が行う
.PHONY: image
image:
	docker build -f deploy/Dockerfile --build-arg REVISION=$$(git rev-parse --short HEAD) -t isuxportal:local .

.PHONY: rspec-image
rspec-image:
	docker build -t isuxportal-rspec rspec

# 例: make rspec RSPEC_ARGS='spec/bench_spec.rb'
.PHONY: rspec
rspec: db-up rspec-image
	env $(GO_TEST_ENV) ./scripts/run_rspec.sh $(RSPEC_ARGS)
