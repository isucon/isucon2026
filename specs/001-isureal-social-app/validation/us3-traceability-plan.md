# US3 事前トレーサビリティ計画

| 受入キー | 検証 |
|---|---|
| US3-AS01..AS11, AS14 | `backend/tests/integration/interactions_test.go` |
| US3-AS12..AS13 | `backend/tests/integration/interaction_pagination_test.go` |
| 競合・再試行 | `backend/tests/concurrency/interactions_test.go` |
| API形状 | `backend/tests/contract/interactions_test.go` |
| US3-AS15 とWeb操作 | `frontend/tests/e2e/interactions.spec.ts` |
