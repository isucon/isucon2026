# Quickstart execution record

**対象タスク**: T077  
**状態**: PASS  
**実行日**: 2026-08-04 (UTC)

## Environment

- Go 1.26.5
- Node.js 24.14.1 / npm 11.11.0
- Buf 1.72.0 / protoc 35.1
- Playwright 1.62.1 / Chrome 151.0.7922.34
- Docker 29.7.1 / Compose v5.4.0

## Scenario results

| Quickstart area | Result | Evidence |
|---|---|---|
| contract lint/build/generation | PASS | Buf lint/build、再生成差分なし |
| Go/Web build and tests | PASS | `make portal-check` |
| default stand-in startup/UI | PASS | independent Compose healthy |
| initial state/countries | PASS | GetState=AP/AP/AP, server1, JP、10か国 tests |
| stream/convergence | PASS | contract/integration and settings E2E |
| invalid input / TeamKey | PASS | contract/integration suites |
| idempotent UpdateDesired | PASS | store/contract tests |
| benchmark/lock/history | PASS | integration/race and Playwright |
| no-report/fail-once profiles | PASS | stand-in unit/integration tests |
| external-router/report semantics | PASS | external_router/router_validation tests |
| reset/determinism | PASS | store/benchmark/failure recovery tests |
| Playwright | PASS | 7/7 |
| Compose smoke | PASS | SPA、health、proxied Connect GetState |

仕様の対話的 `buf curl` と同じ公開RPC経路は生成Connect clientによるcontract testおよび
Web proxy経由のJSON Connect smokeで検証した。未解決項目なし。
