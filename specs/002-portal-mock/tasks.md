# Tasks: ISUCON2026 開発用モックポータル（1チーム前提）

**Input**: Design documents from `specs/002-portal-mock/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/README.md](contracts/README.md),
[quickstart.md](quickstart.md), [design/README.md](design/README.md)

**Validation**: 本仕様、Constitution 2.0.0、`research.md` R-12 が unit、Connect contract、
integration、concurrency、Vitest、Playwright、生成差分、初期化再現性の自動検証を要求するため、
各ユーザーストーリーでテストを先に作成し、実装前に期待どおり失敗することを確認する。

**Specification stage**: 開発補助モックの支援ツール仕様。ISUReal 本体のアプリ基準仕様・競技仕様とは
独立した成果物として正しい Portal/Router 契約、決定的初期化、UI シミュレーションを実装する。
実負荷、採点式、意図的ボトルネック、想定解、複数チーム、永続化、実ベンチマーカー連携は対象外とし、
公開契約と検証成果物を後続仕様へ版固定して引き渡す。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 直前までの依存を満たした後、異なるファイルを変更する他の `[P]` タスクと並行実行可能
- **[Story]**: `US1`〜`US3` は `spec.md` のユーザーストーリーに対応
- すべてのタスクに実際の変更先ファイルを明記する
- 各ストーリーの実装開始前に、受け入れシナリオと必要な edge case を
  `validation/usn-traceability-plan.md` へ割り当て、対応する Validation タスクで未実装動作の期待失敗を確認する

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: ISUReal 本体から独立した Go/ConnectRPC サーバー、React SPA、共有生成コード、
単体起動構成の骨格を作る。

- [X] T001 Create the independent portal mock server and web directory skeleton with package documentation in `portal-mock/server/cmd/portal/main.go`, `portal-mock/server/internal/doc.go`, `portal-mock/server/tests/doc.go`, and `portal-mock/web/src/app/App.tsx`
- [X] T002 Initialize the shared generated Go module and Go 1.26.5 portal server module with Connect-Go, Protobuf-Go, UUID, and local `gen/go` replacement dependencies in `gen/go/go.mod`, `portal-mock/server/go.mod`, and `portal-mock/server/go.sum`
- [X] T003 [P] Initialize the React 19.2/TypeScript 5.9/Vite 8 application with Connect-ES, Protobuf-ES, Vitest, Testing Library, Playwright, and an exact dependency lock in `portal-mock/web/package.json` and `portal-mock/web/package-lock.json`
- [X] T004 [P] Configure TypeScript, Vite proxying, ESLint, Vitest, and Playwright for the independent SPA in `portal-mock/web/tsconfig.json`, `portal-mock/web/vite.config.ts`, `portal-mock/web/eslint.config.js`, `portal-mock/web/vitest.config.ts`, and `portal-mock/web/playwright.config.ts`
- [X] T005 [P] Define safe local defaults for team number, hostname hash, domain suffix, router mode, stand-in profile, apply timeout, benchmark timings, and HTTP listen address in `portal-mock/.env.example` and `portal-mock/compose.yaml`
- [X] T006 [P] Add portal contract generation, server format/vet/test/build, web lint/typecheck/test/build/E2E, compose, and full-check commands in `Makefile`
- [X] T007 [P] Extend repository ignore rules for portal server binaries, portal web output, Playwright artifacts, and local portal configuration in `.gitignore` and `.dockerignore`
- [X] T008 [P] Create the SPA HTML entrypoint and generated TypeScript package metadata without editing generated descriptors in `portal-mock/web/index.html` and `gen/ts/package.json`

**Checkpoint**: `portal-mock/server` と `portal-mock/web` を本体とは別モジュールとして依存解決できる。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 全ストーリーをブロックする Protobuf 契約、値型、初期状態、同期メモリストア、
stream 配信、RPC/テスト基盤を実装する。

**⚠️ CRITICAL**: このフェーズ完了前にユーザーストーリー実装へ進まない。

- [X] T009 Write failing contract and generation-drift checks for `portal.v1` and `portal_mock.v1`, local-only plugins, descriptor compilation, and generated Go/TypeScript output in `portal-mock/server/tests/contract/proto_contract_test.go` and `portal-mock/web/scripts/check-generated.mjs`
- [X] T010 Finalize the versioned Router and mock UI Protobuf contracts, Buf lint/breaking policy, and local managed generation configuration in `proto/portal/v1/portal.proto`, `proto/portal_mock/v1/portal_mock.proto`, `buf.yaml`, and `buf.gen.yaml`
- [X] T011 Regenerate committed Go and TypeScript descriptors/Connect handlers and verify their module boundaries in `gen/go/portal/v1/portal.pb.go`, `gen/go/portal/v1/portalv1connect/portal.connect.go`, `gen/go/portal_mock/v1/portal_mock.pb.go`, `gen/go/portal_mock/v1/portal_mockv1connect/portal_mock.connect.go`, `gen/ts/portal/v1/portal_pb.ts`, and `gen/ts/portal_mock/v1/portal_mock_pb.ts`
- [X] T012 [P] Write failing configuration tests for defaults, invalid team numbers, invalid modes/profiles, mode/profile compatibility, durations, and reset-stable hostname settings in `portal-mock/server/internal/config/config_test.go`
- [X] T013 Implement typed environment loading and startup validation for all portal runtime settings in `portal-mock/server/internal/config/config.go`
- [X] T014 [P] Write failing domain tests for Region, ServerID, TeamKey, Config completeness, the ten supported countries, country-code validation, and calculated hostnames in `portal-mock/server/internal/domain/config_test.go`
- [X] T015 Implement Region/ServerID/TeamKey/Config value validation, the ten-country catalog, and hostname/default-country-region calculation in `portal-mock/server/internal/domain/config.go` and `portal-mock/server/internal/domain/countries.go`
- [X] T016 [P] Write failing state tests for stand-in/external-router initial states, desired/applied separation, diagnostic invariants, unique snapshot IDs, and reset-stable team hostnames in `portal-mock/server/internal/domain/state_test.go`
- [X] T017 Implement ConfigSnapshot, AppliedConfig, ApplyDiagnostic, PortalState, mock runtime, and deterministic initial-state construction in `portal-mock/server/internal/domain/state.go`
- [X] T018 [P] Implement injectable clocks, snapshot/run ID generators, and deterministic fakes in `portal-mock/server/internal/clock/clock.go`, `portal-mock/server/internal/clock/fake.go`, `portal-mock/server/internal/id/generator.go`, and `portal-mock/server/internal/id/fake.go`
- [X] T019 Write failing store unit/concurrency tests for atomic desired/applied transitions, no-op preservation, report validation, reset priority, stale asynchronous work cancellation, and state invariants in `portal-mock/server/internal/store/store_test.go` and `portal-mock/server/tests/concurrency/store_test.go`
- [X] T020 Implement the synchronized in-memory state store, generation-scoped cancellation, desired/applied transition methods, reset, snapshots, and lock-safe readers in `portal-mock/server/internal/store/store.go`
- [X] T021 [P] Write failing subscription tests for initial delivery, latest-state coalescing, cancellation, slow consumers, reset delivery, and no event for idempotent updates in `portal-mock/server/internal/store/subscription_test.go`
- [X] T022 Implement independent desired/state subscription hubs with bounded latest-value coalescing in `portal-mock/server/internal/store/subscription.go`
- [X] T023 [P] Implement Protobuf/domain conversion, timestamp handling, Connect error mapping, and validation-message redaction in `portal-mock/server/internal/rpc/convert.go` and `portal-mock/server/internal/rpc/errors.go`
- [X] T024 [P] Create reusable fake-clock, deterministic-ID, Connect client/server, stream assertion, and eventual-state test harnesses in `portal-mock/server/tests/testkit/testkit.go`
- [X] T025 Assemble the Connect mux, CORS policy for the local SPA, health endpoint, static SPA fallback, and graceful shutdown in `portal-mock/server/internal/rpc/server.go` and `portal-mock/server/cmd/portal/main.go`
- [X] T026 [P] Create the Connect-ES transport, generated clients, stream lifecycle hook, state context, error presentation, and application shell in `portal-mock/web/src/rpc/client.ts`, `portal-mock/web/src/app/usePortalState.ts`, `portal-mock/web/src/app/PortalStateContext.tsx`, and `portal-mock/web/src/app/App.tsx`

**Checkpoint**: Foundation ready。単一チームの状態を同期的に保持し、RPC と UI の各ストーリーを
共通ストア・生成型・決定的テスト基盤上で実装できる。

---

## Phase 3: User Story 1 - 地理分散設定を切り替えて確定させる (Priority: P1) 🎯 MVP UI

**Goal**: 3サーバーのリージョン、メイン接続先、10か国のデフォルト国を変更し、
desired/applied の反映待ち・失敗・遅延・確定を日本語 UI で観察できる。

**Independent Test**: stand-in の各 profile を使って設定変更を行い、desired が即時更新され、
Router役の成功報告まで applied が維持されること、失敗・遅延でも desired が戻らないこと、
同値更新と不正国コードが状態と stream を変更しないことを API とブラウザから確認する。

### Validation for User Story 1 (write first and verify failure)

- [X] T027 [P] [US1] Map US1 acceptance scenarios, FR-004..FR-010, FR-015..FR-021, FR-029..FR-031, SC-001, SC-006, SC-009, and applicable edge cases to planned automated tests in `specs/002-portal-mock/validation/us1-traceability-plan.md`
- [X] T028 [P] [US1] Write failing Connect contract tests for GetState, StreamState, ListSupportedCountries, UpdateDesired, Reset, empty/invalid patches, unsupported countries, and `changed=false` no-op responses in `portal-mock/server/tests/contract/mock_config_service_test.go`
- [X] T029 [P] [US1] Write failing integration tests for server-region/main-server/default-country changes, calculated target hostnames, pending-to-converged transitions, failure/delay diagnostics, desired non-reversion, and no-op stream silence in `portal-mock/server/tests/integration/config_flow_test.go`
- [X] T030 [P] [US1] Write failing stand-in tests for success, fail-once-then-success with the same snapshot, no-report, delayed reporting, and reset cancellation in `portal-mock/server/internal/standin/standin_test.go`
- [X] T031 [P] [US1] Write failing Vitest tests for desired/applied rendering, pending/delayed/failed/invalid diagnostics, settings lock feedback, all ten countries, and the on-demand country mapping panel in `portal-mock/web/tests/unit/settings.test.tsx`
- [X] T032 [P] [US1] Write failing Playwright journeys for settings change to pending/converged, fail-once retry, no-report delay, unsupported-country rejection, idempotent update, and reset in `portal-mock/web/tests/e2e/settings.spec.ts`

### Implementation for User Story 1

- [X] T033 [US1] Implement GetState, StreamState, ListSupportedCountries, UpdateDesired, and Reset handlers with empty-patch/input/lock error mapping and no-op event suppression in `portal-mock/server/internal/rpc/mock_config_handler.go`
- [X] T034 [US1] Implement pending-timeout evaluation and recovery among CONVERGED, PENDING, DELAYED, FAILED_RETRYABLE, UNSYNCED, and INVALID_REPORT in `portal-mock/server/internal/store/diagnostic.go`
- [X] T035 [US1] Implement the mutually exclusive deterministic success, fail-once-then-success, and no-report stand-in profiles using generation-scoped cancellation in `portal-mock/server/internal/standin/standin.go`
- [X] T036 [P] [US1] Implement keyboard-accessible main-server and server1/server2/server3 region settings with desired/applied values and lock-safe controls in `portal-mock/web/src/components/SettingsCard.tsx`
- [X] T037 [P] [US1] Implement the ten-country selector and on-demand Japanese country/region mapping panel in `portal-mock/web/src/components/CountrySelector.tsx` and `portal-mock/web/src/components/CountryHelp.tsx`
- [X] T038 [P] [US1] Implement convergence badges, desired/applied comparison, snapshot correlation, failure reason, and delayed/invalid-report diagnostics in `portal-mock/web/src/components/ApplyStatus.tsx`
- [X] T039 [P] [US1] Implement the Japanese header Team display, hostname summary, reset control, and mock-only footer notice in `portal-mock/web/src/components/PortalHeader.tsx` and `portal-mock/web/src/components/PortalFooter.tsx`
- [X] T040 [US1] Wire state streaming, mutations, reset feedback, and connection recovery into the portal page in `portal-mock/web/src/app/PortalPage.tsx` and `portal-mock/web/src/app/App.tsx`
- [X] T041 [US1] Recreate the reference dashboard layout, oklch palette, Noto Sans JP/IBM Plex Mono typography, responsive cards, focus styles, and diagnostic states in `portal-mock/web/src/styles/portal.css`
- [X] T042 [US1] Register the mock service and stand-in lifecycle with startup/shutdown handling in `portal-mock/server/internal/rpc/server.go` and `portal-mock/server/cmd/portal/main.go`
- [X] T043 [US1] Run the US1 contract, integration, stand-in, Vitest, and Playwright suites and record commands/results in `specs/002-portal-mock/validation/us1.md`

**Checkpoint**: 開発者が Portal UI から設定を変更し、desired/applied の差分と収束を単独デモできる。

---

## Phase 4: User Story 2 - router が desired を購読し applied を報告する (Priority: P1) 🎯 MVP Router Contract

**Goal**: Router が `portal.v1` の Streaming Pull で最新 desired に収束し、成功/失敗を報告して
Portal の applied と診断を正しく更新できる。

**Independent Test**: external-router モードで generated client または Buf curl を使い、
接続/再接続時の最新 desired、更新配信、妥当な成功・失敗、未知/古い snapshot、異なる Config、
不正 Config、TeamKey エラー、reset 後の継続 stream を UI なしで確認する。

### Validation for User Story 2 (write first and verify failure)

- [X] T044 [P] [US2] Map US2 acceptance scenarios, FR-003, FR-007, FR-010, FR-016, FR-017, FR-022..FR-024, FR-026..FR-028, FR-031, SC-002, SC-007, SC-008, SC-010, and Router edge cases to planned automated tests in `specs/002-portal-mock/validation/us2-traceability-plan.md`
- [X] T045 [P] [US2] Write failing Connect contract tests for StreamDesired initial/update/reconnect/reset delivery, latest-value coalescing, cancellation, and unsupported v1 routes in `portal-mock/server/tests/contract/router_stream_test.go`
- [X] T046 [P] [US2] Write failing Connect contract tests for successful, duplicate, unknown-snapshot, mismatched-config, backward, failure, malformed-oneof, and empty-field ReportApplied requests in `portal-mock/server/tests/contract/router_report_test.go`
- [X] T047 [P] [US2] Write failing tests for INVALID_ARGUMENT invalid Config reports, INVALID_REPORT diagnostics, valid-report recovery, team_number=0, foreign positive TeamKey, and zero state/subscription mutations on rejection in `portal-mock/server/tests/integration/router_validation_test.go`
- [X] T048 [P] [US2] Write failing concurrency tests for slow stream consumers, rapid desired coalescing, reset during streaming/reporting, duplicate sequential reports, and report-versus-update consistency in `portal-mock/server/tests/concurrency/router_stream_test.go`
- [X] T049 [P] [US2] Write failing external-router integration tests for unsynced startup/reset, absent stand-in reports, generated-client convergence, and stale pre-reset success reports in `portal-mock/server/tests/integration/external_router_test.go`

### Implementation for User Story 2

- [X] T050 [US2] Implement PortalRouterService StreamDesired and ReportApplied handlers with TeamKey validation, empty success response, domain validation, and Connect code mapping in `portal-mock/server/internal/rpc/router_handler.go`
- [X] T051 [US2] Implement accepted success/failure report transitions, INVALID_REPORT recovery, correlation-preserving backward/unknown reports, and current-desired-only failure diagnostics in `portal-mock/server/internal/store/report.go`
- [X] T052 [US2] Enforce startup-only stand-in/external-router exclusivity and external-router unsynced initialization/reset behavior in `portal-mock/server/internal/config/config.go`, `portal-mock/server/internal/domain/state.go`, and `portal-mock/server/cmd/portal/main.go`
- [X] T053 [US2] Register the versioned Router service route and verify Router messages never expose mock runtime or calculated UI fields in `portal-mock/server/internal/rpc/server.go` and `portal-mock/server/internal/rpc/convert.go`
- [X] T054 [US2] Run the US2 contract, integration, concurrency, and generated-client suites and record commands/results in `specs/002-portal-mock/validation/us2.md`

**Checkpoint**: `portal.v1` だけを知る Router が Portal のモック固有型へ依存せずに最新 desired へ収束できる。

---

## Phase 5: User Story 3 - ベンチマーク実行を模擬し結果を観察する (Priority: P2)

**Goal**: 収束状態から決定的なベンチマークシミュレーションを開始し、進捗、設定ロック、
PASS/FAIL、スコア、展開ログ、履歴、最高/直近スコア、実行回数を観察できる。

**Independent Test**: fixed fake clock と reset を使い、idle→pending→running→idle、
未収束/二重開始の拒否、全設定変更ロック、履歴先頭追加、スコア更新、reset 前処理のキャンセル、
同一 seed の結果列再現を API とブラウザから確認する。

### Validation for User Story 3 (write first and verify failure)

- [X] T055 [P] [US3] Map US3 acceptance scenarios, FR-008, FR-011..FR-014, FR-022, FR-023, FR-025, SC-003..SC-005, and benchmark/reset edge cases to planned automated tests in `specs/002-portal-mock/validation/us3-traceability-plan.md`
- [X] T056 [P] [US3] Write failing unit tests for the fixed-seed PASS/FAIL sequence, scores, logs, sequence reset, immutable run target snapshot, and score summary calculation in `portal-mock/server/internal/benchmark/generator_test.go`
- [X] T057 [P] [US3] Write failing integration tests for StartBenchmark preconditions, pending/running progress, all-setting lock, completion history order, score cards, and repeated deterministic reset in `portal-mock/server/tests/integration/benchmark_test.go`
- [X] T058 [P] [US3] Write failing concurrency tests for competing starts, update-versus-start ordering, reset during pending/running, canceled timer immunity, and consistent state reads during progress in `portal-mock/server/tests/concurrency/benchmark_test.go`
- [X] T059 [P] [US3] Write failing Vitest tests for idle/pending/running controls, progress, rejection reasons, score cards, newest-first history, PASS/FAIL badges, and expandable logs in `portal-mock/web/tests/unit/benchmark.test.tsx`
- [X] T060 [P] [US3] Write failing Playwright journeys for pending start rejection, running lock, progress/completion, history/log expansion, score updates, reset cancellation, and deterministic result replay in `portal-mock/web/tests/e2e/benchmark.spec.ts`

### Implementation for User Story 3

- [X] T061 [P] [US3] Implement the fixed-seed deterministic benchmark result generator containing both PASS and FAIL outcomes in `portal-mock/server/internal/benchmark/generator.go`
- [X] T062 [US3] Implement the cancelable pending/running progress runner and immutable applied target capture in `portal-mock/server/internal/benchmark/runner.go`
- [X] T063 [US3] Implement atomic benchmark start/progress/complete transitions, setting locks, history prepend, score summaries, and reset sequence restoration in `portal-mock/server/internal/store/benchmark.go`
- [X] T064 [US3] Implement StartBenchmark RPC behavior and integrate benchmark-aware Reset responses/state events in `portal-mock/server/internal/rpc/benchmark_handler.go` and `portal-mock/server/internal/rpc/mock_config_handler.go`
- [X] T065 [P] [US3] Implement highest/latest score and execution-count cards with unset-state presentation in `portal-mock/web/src/components/ScoreCards.tsx`
- [X] T066 [P] [US3] Implement the benchmark start panel, status pill, progress display, and precondition/lock feedback in `portal-mock/web/src/components/BenchmarkPanel.tsx`
- [X] T067 [P] [US3] Implement newest-first benchmark history rows with target metadata, PASS/FAIL, score, timestamps, and keyboard-accessible expandable logs in `portal-mock/web/src/components/BenchmarkHistory.tsx`
- [X] T068 [US3] Wire benchmark mutation, progress streaming, history, score cards, and reset cancellation feedback into `portal-mock/web/src/app/PortalPage.tsx`
- [X] T069 [US3] Run the US3 unit, integration, concurrency, Vitest, and Playwright suites and record commands/results in `specs/002-portal-mock/validation/us3.md`

**Checkpoint**: 全3ストーリーを個別および統合状態で検証できる。

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: 独立配布、契約互換性、アクセシビリティ、CI再現性、トレーサビリティ、
後続仕様への版固定を仕上げる。

- [X] T070 Perform a cross-cutting architecture review of desired/applied SoT separation, lock ordering, generation cancellation, stream backpressure, generated-code boundaries, and ISUReal isolation in `specs/002-portal-mock/validation/architecture-review.md`
- [X] T071 [P] Add cross-story failure/recovery tests for invalid report then reset, delayed stand-in during benchmark attempts, stream disconnect/reconnect, RPC cancellation, and process-style reinitialization in `portal-mock/server/tests/integration/failure_recovery_test.go`
- [X] T072 [P] Add keyboard-only, visible-focus, Japanese-label, responsive-layout, and desired/applied semantic accessibility coverage in `portal-mock/web/tests/e2e/accessibility.spec.ts`
- [X] T073 Create reproducible independent server/web container images and SPA proxy configuration without adding portal routes to the ISUReal compose stack in `portal-mock/server/Dockerfile`, `portal-mock/web/Dockerfile`, `portal-mock/web/nginx.conf`, and `portal-mock/compose.yaml`
- [X] T074 [P] Document local/mise/compose startup, all environment variables, three stand-in profiles, external Router usage, Buf curl examples, reset semantics, and the explicit no-real-benchmark boundary in `portal-mock/README.md`
- [X] T075 Add pinned portal proto lint/build/generate-drift/breaking, Go format/vet/unit/contract/integration/concurrency/build, web lint/typecheck/Vitest/build/Playwright, compose smoke, and secret-scan jobs in `.github/workflows/portal-mock-ci.yml` and align local aggregate commands in `Makefile`
- [X] T076 Reconcile all three pre-implementation traceability plans with completed tests and map FR-001..FR-031, SC-001..SC-010, acceptance scenarios, required edge cases, and quickstart sections in `specs/002-portal-mock/validation/traceability.md`
- [X] T077 Execute every scenario in `specs/002-portal-mock/quickstart.md` from a clean environment and record commands, pinned tool/browser versions, modes/profiles, and results in `specs/002-portal-mock/validation/quickstart-results.md`
- [X] T078 Audit tracked source, generated Protobuf output, Go/web build artifacts, container contexts, examples, and documentation for secrets, personal data, real benchmark code, bottleneck/solution spoilers, BSR remote dependencies, and generated version drift in `specs/002-portal-mock/validation/public-artifact-audit.md`
- [X] T079 Record the `portal.v1`/`portal_mock.v1` contract hashes, generated artifacts, tool versions, test-suite revision, supported-country table, mode/profile behavior, and downstream real-Router/competition handoff boundary in `specs/002-portal-mock/validation/version-manifest.md` and `specs/002-portal-mock/validation/competition-handoff.md`
- [X] T080 Run the complete local and CI-equivalent validation suite and record the final zero-failure result with no unresolved architecture/audit findings in `specs/002-portal-mock/validation/final-validation.md`

**Final Checkpoint**: 独立モックポータル、公開 v1 契約、決定的テスト・初期化、参照デザイン準拠 UI、
版固定された後続引渡し成果物が完成する。実 Router 内部、実ベンチマーカー、採点・負荷・ボトルネックは
別 feature specification でのみ開始する。

---

## Dependencies & Execution Order

### Phase dependencies

```text
Phase 1 Setup
  └─> Phase 2 Foundation (全ストーリーをブロック)
        └─> Phase 3 US1 (P1 UI/desired flow)
              └─> Phase 4 US2 (P1 Router contract)
                    └─> Phase 5 US3 (P2 benchmark/lock)
                          └─> Phase 6 Polish/Handoff
```

US1 と US2 は同じ P1 だが、実装順は shared store の設定変更/状態表示を先に完成させ、
その state transition を外部 Router 向け `portal.v1` として公開する。MVP は US1 単独ではなく、
Foundation + US1 + US2 の Router 契約最小セットで成立する。

### User story dependencies

- **US1 (P1)**: Foundation 後に開始可能。stand-in は Foundation の共通 report transition を利用し、
  外部 Router RPC の完成を待たずに独立検証する。
- **US2 (P1)**: Foundation と US1 の desired 更新・状態診断に依存する。UI には依存せず、
  generated Go client/Buf curl だけで独立検証できる。
- **US3 (P2)**: Foundation と収束判定が完成した US1/US2 に依存する。ベンチ開始可否と実行対象は
  Router 報告済み applied のみから決める。
- **Phase 6**: 全ストーリー完了後。T070 のレビューと T071-T075 の横断検証・配布整備を終えてから、
  T076-T080 でトレーサビリティ、clean quickstart、監査、版固定、最終検証を確定する。

### Within each user story

1. Traceability plan と Validation タスクを作成し、実装前に期待どおり失敗することを確認する。
2. Domain/store behavior を実装する。
3. Connect handler と runtime integration を実装する。
4. React component と state integration を実装する。
5. ストーリー固有の全テストを通し、validation 記録を残す。

---

## Parallel Opportunities

- Phase 1 は T003-T008 を Go module 初期化 T002 と異なるファイルで並行実行できる。
- Phase 2 は config、domain、clock/ID、subscription、RPC conversion、testkit、web shell の `[P]`
  タスクを、各対応実装の依存成立後に並行実行できる。
- 各ストーリーでは traceability、contract、integration/concurrency、Vitest、Playwright の
  Validation タスクを異なるファイルで並行作成できる。
- US1 の Settings/Country/ApplyStatus/Header/Footer、US3 の score/benchmark/history component は
  各 server API が固定された後に並行実装できる。
- `portal-mock/server/internal/rpc/server.go`、`portal-mock/server/cmd/portal/main.go`、
  `portal-mock/web/src/app/App.tsx`、`portal-mock/web/src/app/PortalPage.tsx`、
  `portal-mock/compose.yaml`、`Makefile` は複数フェーズで共有するため、該当タスクを直列にマージする。

## Parallel Example: User Story 1

```text
Task T027: specs/002-portal-mock/validation/us1-traceability-plan.md
Task T028: portal-mock/server/tests/contract/mock_config_service_test.go
Task T029: portal-mock/server/tests/integration/config_flow_test.go
Task T030: portal-mock/server/internal/standin/standin_test.go
Task T031: portal-mock/web/tests/unit/settings.test.tsx
Task T032: portal-mock/web/tests/e2e/settings.spec.ts

After server contracts stabilize:
Task T036: portal-mock/web/src/components/SettingsCard.tsx
Task T037: portal-mock/web/src/components/CountrySelector.tsx and CountryHelp.tsx
Task T038: portal-mock/web/src/components/ApplyStatus.tsx
Task T039: portal-mock/web/src/components/PortalHeader.tsx and PortalFooter.tsx
```

## Parallel Example: User Story 2

```text
Task T044: specs/002-portal-mock/validation/us2-traceability-plan.md
Task T045: portal-mock/server/tests/contract/router_stream_test.go
Task T046: portal-mock/server/tests/contract/router_report_test.go
Task T047: portal-mock/server/tests/integration/router_validation_test.go
Task T048: portal-mock/server/tests/concurrency/router_stream_test.go
Task T049: portal-mock/server/tests/integration/external_router_test.go
```

## Parallel Example: User Story 3

```text
Task T055: specs/002-portal-mock/validation/us3-traceability-plan.md
Task T056: portal-mock/server/internal/benchmark/generator_test.go
Task T057: portal-mock/server/tests/integration/benchmark_test.go
Task T058: portal-mock/server/tests/concurrency/benchmark_test.go
Task T059: portal-mock/web/tests/unit/benchmark.test.tsx
Task T060: portal-mock/web/tests/e2e/benchmark.spec.ts

After benchmark RPC stabilizes:
Task T065: portal-mock/web/src/components/ScoreCards.tsx
Task T066: portal-mock/web/src/components/BenchmarkPanel.tsx
Task T067: portal-mock/web/src/components/BenchmarkHistory.tsx
```

---

## Implementation Strategy

### MVP First

1. Complete Phase 1: Setup.
2. Complete Phase 2: Foundational.
3. Complete Phase 3: US1 settings UI and desired/applied visualization.
4. Complete the Phase 4 minimum Router contract: T045-T053.
5. **STOP and VALIDATE**: Run T043 and T054. Demonstrate settings change → pending → Router/stand-in report → convergence.

US1 の価値は Router 報告による確定まで含むため、推奨 MVP は **US1 + US2 の v1 Router 契約**。
ベンチマークシミュレーション（US3）は次の増分とする。

### Incremental Delivery

1. Setup + Foundation → 契約・状態基盤が利用可能。
2. US1 → Portal で設定と desired/applied 診断を操作可能。
3. US2 → external Router が公開 v1 契約で収束可能。ここで MVP 完成。
4. US3 → 決定的ベンチマーク、ロック、履歴、スコアを追加。
5. Polish → 独立 compose、完全 CI、トレーサビリティ、監査、版固定。

### Parallel Team Strategy

1. チームで Setup + Foundation を完了する。
2. 各ストーリーの Validation ファイルは並行作成する。
3. shared store と RPC contract を直列に確定後、server handler と React components を並行実装する。
4. US1/US2 MVP の統合後に US3 を追加し、最後に Phase 6 の配布・監査ゲートを実施する。

---

## Notes

- `[P]` は異なるファイルを変更し、未完了タスクへの依存がない場合だけ並行実行する。
- 生成済みの `gen/go/**` と `gen/ts/**` は `buf generate` の出力としてのみ更新し、手編集しない。
- `portal.v1` は Router 共有契約、`portal_mock.v1` はモック固有契約として責務を混在させない。
- すべての state mutation は同期 store と generation-scoped cancellation を通す。
- 実装タスク開始前に対応するテストが失敗することを確認する。
- 各タスクまたは論理グループごとにコミット可能な粒度を維持する。
- 実 Router 内部の遅延/帯域制御、実ベンチ通信、採点、永続化、認証、複数チームを先取りしない。
