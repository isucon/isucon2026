# Quickstart 実行結果

実行日・browser解決日: 2026-07-19 (UTC)

## 環境

- Linux amd64
- Go `1.26.5`（toolchain auto）、Node `24.14.1`、npm `11.11.0`
- Docker `29.6.2`、PostgreSQL `16.9-alpine`（composeの固定digest）
- Playwright `1.61.1`、Chrome `149.0.7827.55`
- browser image: `mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48`

## clean start

実行コマンド:

```text
docker compose -f deploy/compose.yaml down --volumes
docker compose -f deploy/compose.yaml up -d postgres
ISUREAL_DATABASE_URL=... go -C backend run ./cmd/migrate up
ISUREAL_DATABASE_URL=... go -C backend run ./cmd/seed --fixture minimal  # 2回
npm --prefix frontend ci
go -C backend run ./cmd/api
go -C backend run ./cmd/worker
npm --prefix frontend run dev
```

このプロジェクトの生成済み`isureal_postgres-data`だけを削除し、新規volumeから開始した。削除した内容はテストfixtureのみで復元対象ではなく、直後にminimal fixtureを再生成した。migration versionは7、seedを2回実行して各回同じ4 user/1 admin/1 settingへresetされた。`GET /health/ready`、Vite proxy経由のseed user login、Service Worker source取得はPASS。

実HTTP確認でsession responseの`writeOnly`契約不整合を発見・修正し、OpenAPI clientを再生成した後に同じloginを再実行してPASSした。

## scenario results

| Quickstart | 実行した自動/実HTTP検証 | 結果 |
|---|---|---|
| 1 Start and initialize | 上記clean start、health、proxy login、Service Worker | PASS |
| 2 Automated validation | `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1 && go vet ./...`; frontend typecheck/lint/unit/drift/contract/build/E2E | PASS |
| 3 Core end-to-end | post/feeds/notifications/idempotency integration・concurrency、実login | PASS |
| 4 Visibility and interaction | `feeds_test.go`, `interactions_test.go`, interaction concurrency/E2E | PASS |
| 5 Relationship concurrency | relationship/login/interaction concurrency suites | PASS |
| 6 Post deletion lifecycle | post integration/concurrency、Memories tests | PASS |
| 7 JPEG safety | image unit、sanitizer slot/resource/crash/timeout/secret-isolation、failure recovery | PASS |
| 8 Notifications and Push | notification/push integration、notification concurrency、SW unit/E2E | PASS |
| 9 Region and Memories | notification policy/integration、Memories/profile suites | PASS |
| 10 Account deletion | account deletion integration/concurrency、purge worker tests | PASS |
| 11 Pagination and keyboard | feeds/search/relationship/comment/RealMoji/notification/Memories pagination、Playwright keyboard 2 journeys | PASS |
| 12 Structured logs | `log_audit_test.go`、worker log review、gitleaks | PASS |
| 13 Browser/HTTP security | security integration/Playwright、production nginx container header curl | PASS |

production buildはVAPID公開鍵fixture付きで`dist/service-worker.js`を生成した。固定nginx imageからcontainerを起動し、`/`と`/service-worker.js`がCSP、`Permissions-Policy: camera=(self)`、`Referrer-Policy: no-referrer`、`nosniff`（SWは追加で`no-cache`）を返すことを確認した。

最終結果: Quickstart全scenario PASS。性能スコア・競技較正は実施していない。

## 追記: one-command Compose起動

再検証日: 2026-07-20 (UTC)

既存の開発用volumeには触れず、専用の一時project名、新規volume、別portで次を実行した。

```text
ISUREAL_POSTGRES_PORT=55432 ISUREAL_API_PORT=18080 ISUREAL_FRONTEND_PORT=15173 \
  docker compose -p isureal-compose-test up -d --build --wait
```

PostgreSQL health→migration 7件→minimal fixture→API/worker→frontendの順で起動し、全長期稼働serviceがhealthy/runningとなった。API readiness、frontend HTML、frontend nginx proxy経由のseed user loginを実HTTPで確認してPASS。同じ`docker compose up`を再実行した後もmigration 7件、user 4件、administrator 1件、作成済みlogin session 1件が保持され、初期化serviceが既存データをresetしないこともPASSした。
