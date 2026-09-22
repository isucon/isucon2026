# US2 traceability plan: Router が desired を購読し applied を報告する

**対象タスク**: T044  
**計画状態**: テスト実装・実行結果の統合待ち

| 要件/シナリオ | 期待する観測 | 具体的な自動テストパス |
|---|---|---|
| US2-AS1, FR-024, SC-002 | 接続/再接続初回に最新 desired、更新配信、slow consumer は最新へ coalesce | `server/tests/contract/router_stream_test.go::TestInitialUpdateReconnectAndReset`; `server/tests/concurrency/router_stream_test.go::TestSlowConsumerCoalescesLatest` |
| US2-AS2, FR-017 | 妥当成功の snapshot/config を applied に反映 | `server/tests/contract/router_report_test.go::TestValidSuccessUpdatesApplied` |
| US2-AS3, FR-016 | 失敗報告で applied 維持、現在desiredだけ FAILED_RETRYABLE | `server/tests/contract/router_report_test.go::TestFailureReport`; `server/tests/integration/router_validation_test.go::TestStaleFailureDoesNotOverwriteDiagnostic` |
| US2-AS4, FR-017, SC-007 | 構造/値域不正成功を INVALID_ARGUMENT、applied不変、INVALID_REPORT | `server/tests/integration/router_validation_test.go::TestInvalidConfigReport` |
| US2-AS5/6, FR-003, SC-008 | foreign positive=NOT_FOUND、zero=INVALID_ARGUMENT、状態/購読0件 | `server/tests/integration/router_validation_test.go::TestTeamKeyRejection` |
| FR-007, FR-010 | Router 報告前は applied に desired を混入しない | `server/tests/integration/external_router_test.go::TestUnsyncedStartupHasNoApplied` |
| FR-022, FR-027 | reset 中もstream維持し新snapshot配信、旧snapshot成功も妥当なら反映 | `server/tests/contract/router_stream_test.go::TestResetDelivery`; `server/tests/integration/external_router_test.go::TestStalePreResetSuccessAccepted` |
| FR-023, FR-026 | external-router 起動/reset は UNSYNCED、stand-in reportなし、mode排他 | `server/tests/integration/external_router_test.go::TestExternalRouterLifecycle` |
| FR-028 | `portal.v1` route のみ成立、本文にversion/runtime/calculatedなし | `server/tests/contract/router_stream_test.go::TestUnsupportedVersionRoute`; proto contract test |
| FR-031, SC-010 | INVALID_REPORT 後の妥当成功でreason clear、CONVERGED/PENDING/DELAYED | `server/tests/integration/router_validation_test.go::TestValidReportClearsInvalidDiagnostic` |
| edge: duplicate report | 重複排除せず最後の成功として再設定 | `server/tests/contract/router_report_test.go::TestDuplicateSequentialSuccess` |
| edge: unknown/mismatched/backward | snapshot未知、Config不一致、過去版、applied後退でも妥当なら受理 | `server/tests/contract/router_report_test.go::TestCorrelationMismatchAccepted` |
| edge: malformed oneof/empty fields | INVALID_ARGUMENT、applied不変 | `server/tests/contract/router_report_test.go::TestMalformedReports` |
| edge: stream cancel | client cancelで終了し購読解放 | `server/tests/contract/router_stream_test.go::TestCancellation` |
| edge: rapid update/report/reset競合 | lock順序に従う一貫した状態、raceなし | `server/tests/concurrency/router_stream_test.go` の rapid/reset/report race 群 |

## 完了条件

1. generated Go client または Buf curl と同じ公開 route で検証する。
2. contract/integration/concurrency suite を `-race` を含めて成功させる。
3. 実行記録を `us2.md`、最終対応を `traceability.md` に統合する。
