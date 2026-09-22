# Portal Mock final traceability matrix

**対象タスク**: T076  
**更新日**: 2026-08-04  
**状態**: テストファイル実装・最終実行結果の統合待ち

凡例: US1=`U1`、US2=`U2`、US3=`U3`、横断=`X`。
テスト名は traceability plan の観測単位を表し、実装時に同等の実在名へ照合する。

## Functional requirements

| ID | テスト/検証パス | Quickstart |
|---|---|---|
| FR-001 | `X: compose-smoke`; `architecture-review.md` ISUReal isolation | §4, Compose README |
| FR-002 | `web/tests/e2e/accessibility.spec.ts`; visual/component Playwright | §4, §15 |
| FR-003 | `U2: router_validation TestTeamKeyRejection` | §8.1 |
| FR-004 | `U1: config_flow TestServerRegionPendingThenConverged` | §7 |
| FR-005 | `U1: config_flow TestMainServerCalculatedHostname` | §7 |
| FR-006 | `U1: country contract/unit/e2e` | §7, §8 |
| FR-007 | `U1: pending/converged/failure/delay`; `U2: valid report` | §7, §10, §11 |
| FR-008 | `U1: reset`; `U3: process reinitialization` | §14 |
| FR-009 | `U1: GetState contract`; UI/API equality | §6 |
| FR-010 | `U1: AppliedNeverUsesDesired`; `U2: unsynced startup` | §6, §12 |
| FR-011 | `U3: lifecycle/progress/result` | §9 |
| FR-012 | `U3: pending/double-start rejection` | §9, §10 |
| FR-013 | `U3: AllSettingsLocked`; accessibility | §9 |
| FR-014 | `U3: CompletionSummaryAndHistory` | §9 |
| FR-015 | Japanese labels/footer unit + Playwright | §4 |
| FR-016 | `U1: failure/delay non-reversion`; `U2: failure report` | §10, §11, §13 |
| FR-017 | `U2: report matrix`; invalid/recovery tests | §13 |
| FR-018 | country domain/contract tests for exact 10 | §6, §8 |
| FR-019 | country help Vitest + keyboard E2E | §4 |
| FR-020 | unsupported/malformed country rejection | §8 |
| FR-021 | ListSupportedCountries contract/UI equality | §6 |
| FR-022 | reset integration/concurrency/e2e | §14 |
| FR-023 | mode-specific initial/reset state tests | §6, §12, §14 |
| FR-024 | `U2: router_stream` initial/update/reconnect/coalesce/reset | §7, §12 |
| FR-025 | `U3: deterministic generator/replay` | §14 |
| FR-026 | config + external-router lifecycle tests | §12 |
| FR-027 | reset stream + stale pre-reset report acceptance | §14 |
| FR-028 | proto contract + unsupported route test + breaking CI | §2 |
| FR-029 | stand-in three-profile unit/integration/e2e | §10, §11 |
| FR-030 | no-op contract/integration/stream silence | §8.2 |
| FR-031 | valid-report recovery converged/pending/delayed | §13 |

## Success criteria

| ID | 判定パス |
|---|---|
| SC-001 | US1 API/UI applied equality + timeout non-reversion |
| SC-002 | US2 initial/reconnect/coalesce + success/failure report |
| SC-003 | US3 all-setting lock + completion summary |
| SC-004 | reset/restart value and deterministic sequence comparisons |
| SC-005 | Go/Vitest/Playwright CI, deterministic replay |
| SC-006 | exact catalog acceptance + XX/AP/EU/jp/JPN rejection |
| SC-007 | invalid Config matrix 100% INVALID_ARGUMENT、相関差拒否0件 |
| SC-008 | TeamKey zero/foreign matrix、mutation/subscription 0件 |
| SC-009 | changed=false、snapshot/diagnostic/event変更0件 |
| SC-010 | INVALID_REPORT reason clear、診断再計算 matrix |

## Acceptance scenarios

| Story | Scenario | Primary paths |
|---|---|---|
| US1 | AS1–AS6 | `us1-traceability-plan.md` 全行 |
| US2 | AS1–AS6 | `us2-traceability-plan.md` 全行 |
| US3 | AS1–AS5 | `us3-traceability-plan.md` 全行 |

## Required edge cases

| Edge case | Planned path |
|---|---|
| readとlocked update同時 | benchmark concurrency read-vs-update |
| pending中の連続desired更新 | config flow rapid updates + stream coalesce |
| duplicate success | router report duplicate sequential |
| old/backward/unknown/mismatched success | router report correlation mismatch matrix |
| invalid success | router validation invalid Config matrix |
| INVALID_REPORT recovery | router validation recovery converged/unconverged |
| TeamKey zero/foreign | router validation no-mutation/no-subscription |
| idempotent update | mock contract + both stream event count 0 |
| delayed後のlate success | config flow late success |
| fail-once same-version retry | stand-in test |
| initial state mode difference | domain/external integration |
| reset stream maintenance | router stream reset delivery |
| reset cancels timers | stand-in/benchmark concurrency |
| unsupported v1 route | router contract unsupported route |
| UI/API applied equality | Vitest + Playwright API comparison |
| unsupported country values | contract + E2E input matrix |

## Cross-cutting deliverables

- Container/proxy: `portal-mock/{server,web}/Dockerfile`, `web/nginx.conf`, `compose.yaml`
- CI: `.github/workflows/portal-mock-ci.yml`
- Architecture: `architecture-review.md`
- Clean scenarios: `quickstart-results.md`
- Public audit: `public-artifact-audit.md`
- Version/handoff: `version-manifest.md`, `competition-handoff.md`

最終実行後、存在しないテスト名を実際の名前へ置換し、各SCの100%/0件条件を
`final-validation.md` の結果へリンクする。
