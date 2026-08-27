# ポータル Go rewrite の開発用タスク。
# `make rspec` で MySQL/Redis 起動 → Go サーバ起動 → HTTP 化 RSpec 実行まで一気に行う。

GO_TEST_ENV := PORTAL_TEST_MODE=1 PORTAL_AUTO_MIGRATE=1 PORTAL_LISTEN=:8081 \
	ISUXPORTAL_DB_ADDR=127.0.0.1:13306 \
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

.PHONY: build
build:
	go build ./...

.PHONY: test
test:
	go test ./...

.PHONY: db-up
db-up:
	docker compose up -d --wait mysql redis

.PHONY: db-down
db-down:
	docker compose down -v

.PHONY: server
server: db-up
	$(GO_TEST_ENV) go run ./cmd/portal

.PHONY: rspec-image
rspec-image:
	docker build -t isuxportal-rspec rspec

.PHONY: rspec
rspec: db-up rspec-image
	env $(GO_TEST_ENV) ./scripts/run_rspec.sh
