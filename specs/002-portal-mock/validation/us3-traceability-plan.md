# US3 traceability plan: ベンチマーク実行を模擬し結果を観察する

**対象タスク**: T055  
**計画状態**: テスト実装・実行結果の統合待ち

| 要件/シナリオ | 期待する観測 | 具体的な自動テストパス |
|---|---|---|
| US3-AS1, FR-012 | desired≠applied/applied不明で StartBenchmark は FAILED_PRECONDITION | `server/tests/integration/benchmark_test.go::TestPendingBlocksStart`; `web/tests/e2e/benchmark.spec.ts::pending rejection` |
| US3-AS2, FR-011 | IDLE→PENDING→RUNNING→IDLE、進捗をstream/UI表示 | `server/tests/integration/benchmark_test.go::TestLifecycleAndProgress`; `web/tests/unit/benchmark.test.tsx`; Playwright lifecycle |
| US3-AS3, FR-013, SC-003 | pending/running中の全設定変更を拒否、UI controls lock | `server/tests/integration/benchmark_test.go::TestAllSettingsLocked`; `web/tests/e2e/benchmark.spec.ts::running lock` |
| US3-AS4, FR-014 | 完了履歴を先頭追加、対象snapshot固定、score cards/count更新、ログ展開 | `server/tests/integration/benchmark_test.go::TestCompletionSummaryAndHistory`; `web/tests/unit/benchmark.test.tsx`; Playwright history/log |
| US3-AS5, FR-008, FR-023 | 再起動で設定/idle/空履歴/score未設定へ戻る | `server/tests/integration/failure_recovery_test.go::TestProcessStyleReinitialization` |
| FR-022, SC-004 | pending/running中resetを優先し、旧timer完了を無効化 | `server/tests/concurrency/benchmark_test.go::TestResetCancelsPendingAndRunning`; Playwright reset cancellation |
| FR-025, SC-005 | 固定seedでPASS/FAIL、score、logs列がreset後に完全再現 | `server/internal/benchmark/generator_test.go::TestDeterministicSequence`; `server/tests/integration/benchmark_test.go::TestRepeatedResetSequence`; Playwright replay |
| edge: 二重start | 競合startは1件だけ成功、他は FAILED_PRECONDITION | `server/tests/concurrency/benchmark_test.go::TestCompetingStarts` |
| edge: update vs start | store lock順序で「更新」または「start」の一方だけが整合して成立 | `server/tests/concurrency/benchmark_test.go::TestUpdateVersusStart` |
| edge: 実行対象不変 | 開始時 applied の main/region/hostname を履歴へ固定 | `server/internal/benchmark/generator_test.go::TestImmutableTargetSnapshot` |
| edge: reset反復 | sequence index、履歴、scoreが毎回初期化 | generator/integration の repeated reset tests |
| accessibility | 状態pill、progress、disabled理由、ログ展開がkeyboard/semanticに観測可能 | `web/tests/e2e/accessibility.spec.ts` |

## 完了条件

1. fixed fake clock を使い、wall clock sleep 依存を最小化する。
2. Go unit/integration/concurrency、Vitest、Playwright が成功する。
3. 実行記録を `us3.md`、最終対応を `traceability.md` に統合する。
