# US2 事前トレーサビリティ計画

| 受入キー | 事前に割り当てた検証 |
|---|---|
| US2-AS01..AS08 | `backend/tests/integration/relationships_test.go` |
| US2-AS09, AS11, AS13, AS18, AS20, AS22, AS25 | `backend/tests/integration/user_search_test.go` |
| US2-AS10, AS12, AS14..AS17, AS19, AS21, AS23, AS24 | `backend/tests/concurrency/relationships_test.go` と `backend/internal/domain/relationship_policy_test.go` |
| 全API形状 | `backend/tests/contract/relationships_test.go` |
| Web操作 | `frontend/tests/e2e/relationships.spec.ts` |

この表は実装開始前の割当であり、最終結果は `validation/us2.md` に記録する。
