# Requirements Traceability: 擬似地理分散 Router

**Handoff ID**: `router-contract-v1`

**Date**: 2026-08-04

**Specification stage**: 支援インフラ仕様

**Audit result**: FR-001〜FR-025 / SC-001〜SC-007 PASS

Runtime evidence の測定値と環境は [quickstart-results.md](./quickstart-results.md) を参照する。

## Functional requirements

| Requirement | Source / automated evidence | Result |
|---|---|---:|
| FR-001 | `internal/domain/config.go` の3 ServerID/Region、domain table test、dynamic E2E | PASS |
| FR-002 | `compose.e2e.yaml` の3専用 segment、fixture static route、6方向 route E2E | PASS |
| FR-003 | internal network、server は単一 segment のみ、Router-down 6方向 timeout | PASS |
| FR-004 | `config.go` の 0 ms/1 Gbps default、`tc_test.go` same class assertions | PASS |
| FR-005 | `config.go` の 200 ms RTT/200 Mbps default、`tc_test.go` の片道分割と全 region combination | PASS |
| FR-006 | `xds/routes.go` の app/main・individual mapping、route matrix/E2E | PASS |
| FR-007 | exact country route + absent-header route、default propagation E2E | PASS |
| FR-008 | domain の10か国 mapping test、E2E 10/10 header propagation | PASS |
| FR-009 | invalid catch-all direct response、unsupported/duplicate E2E、lower/empty は同じ catch-all | PASS |
| FR-010 | default `OVERWRITE_IF_EXISTS_OR_ADD`、fixture header values、E2E exact one value | PASS |
| FR-011 | route-level same fault/rate builder test、production default config test | PASS |
| FR-012 | route-level cross fault/rate builder test、実 HTTP delay/rate E2E | PASS |
| FR-013 | `reconcile/portal.go` の Connect stream + positive TeamKey、in-process contract test | PASS |
| FR-014 | protobuf→domain 全体 validation 後に candidate 作成、domain/reconcile tests | PASS |
| FR-015 | network apply→RDS publish→version ACK→active commit の単一 writer transaction | PASS |
| FR-016 | success/failure `ReportApplied`、Portal contract tests、E2E desired=applied | PASS |
| FR-017 | network/controller rollback tests、active snapshot preservation test | PASS |
| FR-018 | bounded exponential reconnect、Portal restart E2E（約6秒） | PASS |
| FR-019 | capacity-1 latest queue、single writer、duplicate/coalesce tests | PASS |
| FR-020 | status health/readiness tests、current-stream Envoy ACK gate、Envoy stop E2E | PASS |
| FR-021 | JSON log、bounded status snapshot/error/retry、任意 body 非記録 | PASS |
| FR-022 | Portal/Router/Envoy/3 fixtures/runner と5 internal network の Compose model | PASS |
| FR-023 | `tests/e2e/main.go` と `run.sh` の全 control/HTTP/network/recovery scenario | PASS |
| FR-024 | 同じ `TrafficProfile.Class` に production default/E2E override を入力、実測 PASS | PASS |
| FR-025 | fixed project、trap diagnostics、exact-project cleanup、既存2 project 維持 | PASS |

## Success criteria

| Criterion | Runtime / test evidence | Result |
|---|---|---:|
| SC-001 | 同一 tree の clean E2E 2回: 71.68秒、68.98秒、両方 PASS | PASS |
| SC-002 | update→converged 約0.20秒、続く100要求がすべて server3/US | PASS |
| SC-003 | HTTP median cross−same: 約121.8 ms、必要値96 ms以上 | PASS |
| SC-004 | HTTP 1.109 Mbps、peer 0.753 Mbps。双方1 Mbps ±35%かつ same より低速 | PASS |
| SC-005 | 6方向 route が Router next hop、Router-down 6/6 timeout | PASS |
| SC-006 | 10か国、欠落、unsupported、duplicate、unknown Host の全 assertion PASS | PASS |
| SC-007 | Portal 約6秒、Envoy 約6秒、Router+Envoy 約8秒で再収束。すべて30秒以内 | PASS |

## Story and edge-case coverage

- US1: Host×country route matrix、default、main switch、same/cross profile を unit + real Envoy で検証。
- US2: nft allow six directions、HTB/netem 全 region class、route/fetch/isolation を unit + real kernel で検証。
- US3: Portal stream/reconnect、latest-wins、duplicate、rollback、report-only retry、readiness を unit + process E2E で検証。
- US4: fixed project の clean build、障害診断、Portal/Envoy/Router restart、cleanup を shell + compiled runner で検証。
- 不明 Host/規定外 Host、invalid country、upstream failure timeout、duplicate snapshot、初回未同期 readiness は direct response、
  unit test、bounded timeout、idempotency、status test へそれぞれ割り当て済み。

## Versioned competition handoff

後続の競技仕様は、次の組を `router-contract-v1` として一体で参照する。これは app baseline や競技負荷・採点を定義せず、
公開 routing/network contract と検証済み支援インフラだけを固定する。

| Item | Pinned value |
|---|---|
| Portal wire contract | `portal.v1`、`proto/portal/v1/portal.proto` SHA-256 `86d21740a45b488129a310a62e22654077f2cfdb4b2006d840deaf5c197efeff` |
| Router dependency manifest | `router/go.mod` SHA-256 `146d5c1637ea59285689c05886d641a3dfa214be6a6f3f0b05a143d926ffe5fe` |
| Envoy runtime/API | Envoy Proxy/API `1.32.2`、go-control-plane `0.13.3` |
| Envoy bootstrap | SHA-256 `f63d30b48fdef9eb409a506f03920a0a6191c150cc128a3e875356bdbaed8c84` |
| Compose topology | SHA-256 `bdb332eb1be862bc0bba9cca5f90e5d00b3f69634149599bf937ce8110f1a257` |
| Domain/country mapping | SHA-256 `663c237de871d8d5bba5b22a6e954a28ea9e09e7d7e709380a48e6ec18b40863` |
| E2E assertions | `main.go` SHA-256 `1495e48989f030d2f0cd45f4ba760b1fcc98e1d29115e7bb0d0c2807d6c0d37f` |
| E2E orchestration | `run.sh` SHA-256 `f62f254d33968a62656a812bf143c4faeb7ffa38af1ff5cee38488ff1f3b23ab` |
| Country mapping | US/CA/MX→US、JP/KR/CN/AU→AP、GB/DE/FR→EU |
| Production profile | same 0 ms/1 Gbps、cross RTT 200 ms（片道100 ms）/200 Mbps |
| Public routing invariant | app→main、serverN Host→serverN、有効国1値、欠落→default、invalid→400、unknown Host→404 |
| Validation command | `make router-check && router/tests/e2e/run.sh` |

Git commit はユーザーの明示依頼がないため作成していない。現在の baseline は
`14a8a0c210a2bcec574aa1cb5db2784a5545ef12` であり、merge/commit 時には本 handoff ID と上記 digest を commit/PR に
記録する。後続競技仕様は、digest またはそれらを含む commit を固定してから負荷、採点、ボトルネック、想定解を定義する。

## Explicitly delegated scope

TLS、WAF、外部認証、HA、長期 log/metric 保存、本番 VPN/物理 NIC、複数 team、benchmark/scoring、意図的 bottleneck、
region-wide aggregate HTTP rate の厳密化は `router-contract-v1` の外であり、後続 deployment/competition spec が所有する。
