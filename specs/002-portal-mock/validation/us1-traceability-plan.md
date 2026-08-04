# US1 traceability plan: 地理分散設定を切り替えて確定させる

**対象タスク**: T027  
**計画状態**: テスト実装・実行結果の統合待ち

| 要件/シナリオ | 期待する観測 | 具体的な自動テストパス |
|---|---|---|
| US1-AS1, FR-004, FR-007 | server2 AP→EU で新 snapshot、pending、stand-in 成功後 applied EU | `server/tests/integration/config_flow_test.go::TestServerRegionPendingThenConverged`; `web/tests/e2e/settings.spec.ts::server region converges` |
| US1-AS2, FR-005, FR-009 | main server3 に変更し applied target hostname が server3 | `server/tests/integration/config_flow_test.go::TestMainServerCalculatedHostname`; `web/tests/unit/settings.test.tsx::renders desired and applied target` |
| US1-AS3, FR-006, FR-018, FR-021 | default US と region US、UI/API 国一覧一致 | `server/tests/contract/mock_config_service_test.go::TestSupportedCountries`; `web/tests/unit/settings.test.tsx::lists ten countries` |
| US1-AS4, FR-007, FR-016, FR-029 | fail-once/no-report で desired 非revert、applied維持、FAILED/DELAYED、同版再試行 | `server/internal/standin/standin_test.go::TestProfiles`; `server/tests/integration/config_flow_test.go::TestFailureAndDelayDoNotRevertDesired`; `web/tests/e2e/settings.spec.ts::profiles` |
| US1-AS5, FR-020, SC-006 | XX/AP/EU/jp/JPN を INVALID_ARGUMENT、状態不変 | `server/tests/contract/mock_config_service_test.go::TestRejectsUnsupportedCountry`; `web/tests/e2e/settings.spec.ts::rejects unsupported country` |
| US1-AS6, FR-030, SC-009 | 同値更新は changed=false、snapshot/診断/両streamイベント不変 | `server/tests/contract/mock_config_service_test.go::TestIdempotentUpdate`; `server/tests/integration/config_flow_test.go::TestNoopDoesNotPublish`; `web/tests/e2e/settings.spec.ts::idempotent update` |
| FR-008, FR-022, FR-023 | reset/再起動で既知初期値、履歴/score初期化、mode別 applied | `server/tests/integration/config_flow_test.go::TestResetInitialState`; `web/tests/e2e/settings.spec.ts::reset` |
| FR-010, SC-001 | UI applied は Router 報告済み値だけ、GetStateと一致 | `server/tests/contract/mock_config_service_test.go::TestAppliedNeverUsesDesired`; `web/tests/unit/settings.test.tsx::separates desired and applied`; Playwright API/UI比較 |
| FR-015 | 日本語ラベルとモック注記 | `web/tests/unit/settings.test.tsx::JapaneseLabels`; `web/tests/e2e/accessibility.spec.ts::mock notice` |
| FR-017, FR-031 | INVALID_REPORT 表示、妥当成功後 reason clear と診断再計算 | `server/tests/integration/router_validation_test.go::TestInvalidReportRecovery`; `web/tests/unit/settings.test.tsx::invalid report recovery` |
| FR-019 | 国コード・日本語名・region のオンデマンド表示 | `web/tests/unit/settings.test.tsx::country help`; keyboard Playwright |
| edge: 反映待ち中の連続変更 | latest desired に集約して snapshot を混同しない | `server/tests/integration/config_flow_test.go::TestRapidUpdatesConvergeLatest` |
| edge: 遅延後の成功 | DELAYED から CONVERGED、desired/applied不整合を残さない | `server/tests/integration/config_flow_test.go::TestLateSuccessConverges` |
| edge: 実行中同時取得/変更 | 読取は整合、変更は FAILED_PRECONDITION | `server/tests/concurrency/benchmark_test.go::TestReadVersusLockedUpdate` |

## 完了条件

1. 上記パスが実在し、テスト名が同等の観測を行う。
2. US1 の server contract/integration/stand-in、Vitest、Playwright が成功する。
3. 実行記録を `us1.md` に残し、最終対応を `traceability.md` へ転記する。
