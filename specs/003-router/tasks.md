# Tasks: 擬似地理分散 Router

**Input**: Design documents from `/specs/003-router/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/README.md`, `quickstart.md`

**Specification Stage**: 支援インフラ仕様（アプリ基準仕様・競技仕様とは独立）

**Validation**: 全 behavior change に automated test を用意し、kernel／Envoy を必要とする境界は専用 Compose E2E で検証する。

**Organization**: User story ごとにテストを先行し、最後に全 story を専用 topology で統合する。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 別ファイルで依存なしに実行可能
- **[Story]**: `spec.md` の User Story 対応
- 全 task に対象 path を明記する

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 独立 Router module と固定 tool/image 境界を作る。

- [X] T001 Create the Go 1.26.5 router module and pin ConnectRPC, go-control-plane, Envoy API, and shared generated-module dependencies in `router/go.mod`
- [X] T002 [P] Create the multi-stage Router image with only `NET_ADMIN` runtime tools in `router/Dockerfile` and extend root `.dockerignore`
- [X] T003 [P] Create the pinned static Envoy listener/xDS/server-cluster bootstrap in `router/envoy/bootstrap.yaml`
- [X] T004 [P] Add router unit, vet, Compose validation, and E2E entry targets to `Makefile`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 全 story が共有する設定、不変条件、診断、side-effect 抽象をテスト先行で作る。

**⚠️ CRITICAL**: この phase 完了まで story 実装へ進まない。

- [X] T005 [P] Write country mapping and RouterConfig validation tests for all 10 countries and invalid protobuf values in `router/internal/domain/config_test.go`
- [X] T006 [P] Write environment/topology/profile boundary tests in `router/internal/config/config_test.go`
- [X] T007 Implement immutable regions, servers, country mapping, desired/active snapshots, and protobuf conversion in `router/internal/domain/config.go`
- [X] T008 Implement strict environment parsing, production traffic defaults, interface-IP topology, hostnames, and backend addresses in `router/internal/config/config.go`
- [X] T009 [P] Define command execution and interface-discovery seams for deterministic tests in `router/internal/netcontrol/system.go`
- [X] T010 [P] Implement synchronized Controller status, structured diagnostic fields, `/healthz`, and `/readyz` in `router/internal/status/status.go` and `router/internal/status/http.go`

**Checkpoint**: Config/domain/status foundation is independently unit-testable.

---

## Phase 3: User Story 1 - 国とホスト名に応じたアプリ接続 (Priority: P1) 🎯 MVP

**Goal**: Host と有効国から target、header、same/cross delay・bandwidth を決定し、Envoy が動的に切り替える。

**Independent Test**: RDS snapshot を Envoy に配信し、4 Host、10 countries、default、invalid/duplicate header、main 切替、
same/cross profile を fixture HTTP 応答で検証する。

### Validation for User Story 1 (MANDATORY) ⚠️

- [X] T011 [P] [US1] Write table-driven RDS route-matrix tests for Host, country/default, target, fault, bandwidth, and invalid direct responses in `router/internal/xds/routes_test.go`
- [X] T012 [P] [US1] Write ACK, NACK, timeout, reconnect, and stale-version tracker tests in `router/internal/xds/ack_test.go`
- [X] T013 [P] [US1] Write fixture identity/blob/header validation tests in `router/test-fixtures/server/main_test.go`

### Implementation for User Story 1

- [X] T014 [US1] Build deterministic RDS RouteConfiguration resources with per-route fault/bandwidth and exact Host/country handling in `router/internal/xds/routes.go`
- [X] T015 [US1] Implement go-control-plane snapshot cache, ADS/RDS gRPC registration, and version-correlated ACK/NACK waiting in `router/internal/xds/server.go` and `router/internal/xds/ack.go`
- [X] T016 [P] [US1] Implement the bounded E2E HTTP fixture identity/blob endpoints in `router/test-fixtures/server/main.go`
- [X] T017 [US1] Validate `router/envoy/bootstrap.yaml` against pinned Envoy and add its listener/filter/cluster assertions to `router/internal/xds/bootstrap_test.go`

**Checkpoint**: HTTP route resources and real Envoy acceptance are testable without server-to-server shaping.

---

## Phase 4: User Story 2 - 分離されたサーバー間通信 (Priority: P2)

**Goal**: 3つの隔離区画間を Router 経由に限定し、region 同異で Linux delay/rate を動的更新する。

**Independent Test**: generated nft/tc candidate を unit test し、3 fixture segment の6方向 route と same/cross transfer を
Compose 上で測定する。

### Validation for User Story 2 (MANDATORY) ⚠️

- [X] T018 [P] [US2] Write nft forward-policy rendering tests covering only the six server-interface directions in `router/internal/netcontrol/nft_test.go`
- [X] T019 [P] [US2] Write HTB/netem class/filter rendering tests for every server-region combination and idempotent order in `router/internal/netcontrol/tc_test.go`
- [X] T020 [P] [US2] Extend fixture tests for bounded peer fetch and `ip route get` output in `router/test-fixtures/server/main_test.go`

### Implementation for User Story 2

- [X] T021 [US2] Implement interface lookup by configured local IPv4 and atomic dedicated nft table reconciliation in `router/internal/netcontrol/nft.go`
- [X] T022 [US2] Implement deterministic HTB/netem batch rendering, apply, verification, and last-known-good rollback in `router/internal/netcontrol/tc.go` and `router/internal/netcontrol/controller.go`
- [X] T023 [P] [US2] Add fixture static peer-route entrypoint and bounded fetch/route endpoints in `router/test-fixtures/server/entrypoint.sh`, `router/test-fixtures/server/Dockerfile`, and `router/test-fixtures/server/main.go`

**Checkpoint**: Linux network controller independently proves Router-only route and same/cross shaping.

---

## Phase 5: User Story 3 - Portal 設定への継続的な収束 (Priority: P3)

**Goal**: Portal desired を latest-wins で transactionally apply/report し、失敗・切断・再起動から自己回復する。

**Independent Test**: fake Portal/xDS/network を使って初期同期、coalesce、rollback、report-only retry、stream reconnect を
決定的に検証し、Portal Mock external-router と実 process の収束を integration test する。

### Validation for User Story 3 (MANDATORY) ⚠️

- [X] T024 [P] [US3] Write latest-wins queue, duplicate, stale generation, apply rollback, and report-only retry tests in `router/internal/reconcile/controller_test.go`
- [X] T025 [P] [US3] Write Portal stream/reconnect/team/error contract tests against an in-process Connect server in `router/internal/reconcile/portal_test.go`
- [X] T026 [P] [US3] Write Controller health/readiness transition tests across initial, applying, active, failed, and disconnected states in `router/internal/status/status_test.go`

### Implementation for User Story 3

- [X] T027 [US3] Implement the versioned Portal Connect client, stream reconnect with bounded backoff, and success/failure report retries in `router/internal/reconcile/portal.go`
- [X] T028 [US3] Implement the single-writer latest-wins apply transaction across network and RDS with rollback and active commit in `router/internal/reconcile/controller.go`
- [X] T029 [US3] Wire strict startup, signal cancellation, xDS/status servers, Portal reconciliation, and structured logging in `router/cmd/router/main.go`

**Checkpoint**: Portal Mock external-router converges to a real Controller and recovers from controlled failures.

---

## Phase 6: User Story 4 - 再現可能な統合検証 (Priority: P4)

**Goal**: clean Compose topology で制御面、HTTP、network、dynamic update、recovery を一つの自動判定にする。

**Independent Test**: `router/tests/e2e/run.sh` が既存 Compose project と衝突せず全シナリオを完了し、専用資源だけを cleanup する。

### Validation for User Story 4 (MANDATORY) ⚠️

- [X] T030 [P] [US4] Write the E2E runner assertions for Portal convergence, HTTP route/header/error, timing/throughput, six peer routes, and peer transfer in `router/tests/e2e/main.go`
- [X] T031 [P] [US4] Define explicit failure-injection assertions for Portal restart, Router/Envoy restart, and Router-down peer isolation in `router/tests/e2e/run.sh`

### Implementation for User Story 4

- [X] T032 [US4] Build the isolated Portal/control, edge, three server-segment topology with fixed IPAM and least capabilities in `router/compose.e2e.yaml`
- [X] T033 [US4] Package the self-contained Go E2E runner in `router/tests/e2e/Dockerfile` and complete scenario modes in `router/tests/e2e/main.go`
- [X] T034 [US4] Implement fail-safe Compose orchestration, diagnostics, restart/isolation phases, cleanup, and keep-on-failure option in `router/tests/e2e/run.sh`

**Checkpoint**: One command proves all four stories against real Envoy and Linux networking.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: 公開設計、実行導線、version handoff、全 gate を仕上げる。

- [X] T035 [P] Expand the architecture, trust boundary, topology, apply/rollback sequence, and operational diagnostics in `docs/router.md`
- [X] T036 [P] Document local commands, environment variables, production defaults, E2E overrides, and limitations in `router/README.md`
- [X] T037 Add Router validation commands to CI in `.github/workflows/router-ci.yml` without coupling to existing application/Portal jobs
- [X] T038 Run unit/vet/bootstrap/Compose validation and full clean E2E from `specs/003-router/quickstart.md`, recording exact evidence in `specs/003-router/validation/quickstart-results.md`
- [X] T039 Audit FR-001〜FR-025 and SC-001〜SC-007 against source/tests/runtime evidence and record versioned competition handoff in `specs/003-router/validation/traceability.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 依存なし。
- **Foundational (Phase 2)**: Setup 後。全 user story を block。
- **US1 (Phase 3)**: Foundational 後。HTTP data plane の MVP。
- **US2 (Phase 4)**: Foundational 後に unit 実装可能。統合時は US1 の fixture Host routing を利用。
- **US3 (Phase 5)**: US1 xDS と US2 network controller の双方に依存。
- **US4 (Phase 6)**: US1〜US3 完了後。
- **Polish (Phase 7)**: 全 story 完了後。

### User Story Dependency Graph

```text
Foundation
  ├── US1 HTTP/xDS ──┐
  └── US2 network ───┼──> US3 reconciliation ──> US4 full E2E
                     └──────────────────────────> US4 fixtures
```

### Within Each User Story

- validation task を先に作り、対応実装前に失敗を確認する。
- pure render/model → side-effect adapter → process integration の順に進める。
- kernel と xDS mutation は単一 writer に限定する。
- story checkpoint で該当 package tests を通してから次へ進む。

### Parallel Opportunities

- T002〜T004、T005〜T006、T009〜T010 は別ファイルで並行可能。
- US1 の T011〜T013、US2 の T018〜T020、US3 の T024〜T026 は各 story 内で並行可能。
- US1 core と US2 pure renderer は Foundation 後に並行可能だが、US3 統合前に双方を完了する。
- T035〜T036 は runtime validation と並行可能。

## Parallel Example: User Story 1

```text
Task T011: router/internal/xds/routes_test.go
Task T012: router/internal/xds/ack_test.go
Task T013: router/test-fixtures/server/main_test.go
```

## Parallel Example: User Story 2

```text
Task T018: router/internal/netcontrol/nft_test.go
Task T019: router/internal/netcontrol/tc_test.go
Task T020: router/test-fixtures/server/main_test.go（T013完了後）
```

## Implementation Strategy

### MVP First

1. Setup + Foundation
2. US1 の RDS/Envoy HTTP path
3. Host/country/main/default/invalid を real Envoy で確認
4. US2 network、US3 reconciliation、US4 E2E を順次統合

### Incremental Delivery

1. Pure config/render tests で contract を固定
2. Envoy HTTP data plane
3. Linux server data plane
4. Portal control plane と rollback
5. Compose failure/recovery E2E
6. CI、document、traceability

## Notes

- production default と E2E override は値だけが異なり、分類 code path は共通。
- Docker socket を test container へ mount しない。restart/stop は host orchestration だけが行う。
- E2E は project name を固定して明示 target を検証してから cleanup する。
- generated `gen/go` は手編集しない。
