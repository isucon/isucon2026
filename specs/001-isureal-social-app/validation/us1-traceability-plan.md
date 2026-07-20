# US1 pre-implementation traceability plan

この計画は US1 実装前に固定する。各受け入れシナリオは以下の自動テストで検証し、T050 で同じ割当の完了結果を記録する。

| Scenario | Planned automated evidence |
|---|---|
| US1-AS01 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS02 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| US1-AS03 | `backend/tests/integration/posts_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS04 | `backend/tests/integration/posts_test.go`（次期間開始による現行フィード除外。Memories 所有者取得は US4） |
| US1-AS05 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/feeds_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS06 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| US1-AS07 | `backend/tests/integration/posts_test.go`, `backend/internal/image/store_test.go` |
| US1-AS08 | `backend/tests/integration/posts_test.go` |
| US1-AS09 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| US1-AS10 | `backend/tests/integration/feeds_test.go` |
| US1-AS11 | `backend/tests/integration/posts_test.go` |
| US1-AS12 | `backend/tests/integration/posts_test.go` |
| US1-AS13 | `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS14 | `backend/tests/integration/posts_test.go` |
| US1-AS15 | `backend/tests/integration/posts_test.go`, `backend/internal/image/store_test.go` |
| US1-AS16 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/feeds_test.go` |
| US1-AS17 | `backend/tests/integration/posts_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |

契約面は `backend/tests/contract/posts_test.go` が current-window、投稿作成・取得・更新・削除、自己現行投稿、4フィードを一意に所有する。
競合・再試行は `backend/tests/concurrency/posts_test.go` が一投稿制約、削除回数、再投稿、冪等な作成/削除を検証する。
