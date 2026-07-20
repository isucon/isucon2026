# CI 検証記録

検証日: 2026-07-19 (UTC)

## 固定バージョン

| 対象 | 固定値 |
|---|---|
| GitHub runner | `ubuntu-24.04` |
| checkout/setup-node/setup-go | 各`v7.0.0`のfull commit SHA（workflow記載） |
| Go | `1.26.5`（`GOTOOLCHAIN=auto`で取得した実行版も`go1.26.5 linux/amd64`） |
| Node / npm | `24.14.1` / `11.11.0` |
| TypeScript / Vite / Vitest | `5.9.3` / `8.1.5` / `4.1.10` |
| OpenAPI tools | Redocly `2.39.0`、openapi-typescript `7.13.0` |
| Playwright / Chrome | `1.61.1` / `149.0.7827.55` |
| browser image | `mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48` |
| PostgreSQL image | `postgres:16.9-alpine@sha256:7c688148e5e156d0e86df7ba8ae5a05a2386aaec1e2ad8e6d11bdf10504b1fb7` |
| gitleaks | `8.30.1`、linux x64 archive SHA-256 `551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb` |

Chrome/browser imageの解決日: 2026-07-19 UTC。

## workflow coverage

`.github/workflows/ci.yml`は以下を独立jobで実行する。

- backend format/vet、domain/service/http/image unit、全contract、API/worker/image-sanitizer/migrate/reset/seed build
- PostgreSQL上の全integration（migration/reset/seed/failure/log/securityを含む）
- PostgreSQL上の全concurrency
- image sanitizer resource/process/secret isolation
- frontend typecheck/lint/unit、OpenAPI lint、generated client drift、production buildとService Worker成果物
- immutable browser image上のChrome exact-version checkと全Playwright
- checksum検証済みgitleaksによる全公開tree secret scan

これにより`validation/traceability.md`で参照した全自動テストが固定CIのいずれかで実行される。

## 最終コードでの実行結果

- `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1 && go vet ./...` (`backend/`): PASS
- `npm run typecheck && npm run lint && npm test && npm run check:openapi && npm run test:contract && VITE_VAPID_PUBLIC_KEY=... npm run build && npm run test:e2e` (`frontend/`): PASS（Playwright 9 tests）
- `go test ./internal/domain -run TestLocationTaggedUnionValidation -count=1`: PASS（US4-AS22補正後）
- `npm run check:browser`: PASS（Chrome `149.0.7827.55`、browser image digest一致）
- pinned gitleaks archive checksum: PASS、`gitleaks dir . --redact`: PASS（0 leaks）
- production frontend container build: PASS、`/`と`/service-worker.js`のCSP/Permissions/Referrer/nosniff headers: PASS
- OpenAPI generated client drift: PASS、Redocly lint: PASS、backend request/response validator tests: PASS

未実行・失敗・未固定のCI gateはない。
