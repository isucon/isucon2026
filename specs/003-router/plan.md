# Implementation Plan: 擬似地理分散 Router

**Branch**: `feature/router` | **Date**: 2026-08-04 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-router/spec.md`

## Summary

> 2026-10-27: HTTP data plane は Envoy と xDS から HAProxy 2.8 に置き換えた（research.md の Decision 8）。
> 以下は当初の計画のまま残す。

Portal の `portal.v1.PortalRouterService` を Streaming Pull する Go 製 Router Controller を独立
`router/` module として追加する。Controller は最新 snapshot を検証し、Linux forwarding／nftables と
HTB+netem の設定を冪等 reconcile すると同時に、Envoy へ RDS snapshot を配信して Host・国別の
上流選択、遅延、帯域制御を更新する。Envoy は Router コンテナと network namespace を共有し、3台の
server はそれぞれ専用 bridge network にだけ接続する。Portal Mock、Router、Envoy、3 server、test runner を
専用 Compose project へ閉じ、制御面・HTTP データ面・server 間データ面・再起動回復を自動 E2E で検証する。

## Technical Context

**Language/Version**: Go 1.26.5、POSIX shell（E2E orchestration と fixture entrypoint）

**Primary Dependencies**: ConnectRPC Go v1.20.0、generated `gen/go` portal v1 client、
Envoy go-control-plane v0.13.3 / Envoy API v1.32.2、Envoy Proxy v1.32.2、Linux `iproute2` / `tc`、nftables

**Storage**: 永続ストレージなし。最新 desired、last-known-good active snapshot、xDS ACK/NACK はプロセスメモリ。

**Testing**: `go test ./...`（domain/rendering/xDS/reconcile unit・integration）、
`docker compose config --quiet`、専用 Compose E2E runner と shell orchestration

**Target Platform**: Linux kernel の network namespace、IPv4 forwarding、HTB、netem、nftables を利用できる
Docker Engine 29.x + Compose v2。amd64/arm64 の official Envoy image を対象。

**Project Type**: system service / control plane + local integration topology

**Performance Goals**: desired 更新から applied 成功報告まで10秒以内。通常転送時の Controller 起因停止なし。
E2E はクリーン build を含め5分以内。

**Constraints**: Router のみに `NET_ADMIN` を付与し `privileged` は使わない。server 間は3つの専用 network を
Router だけが横断する。Envoy の bandwidth filter は信頼済み E2E／競技内部トラフィックだけへ使用する。
xDS ACK は resource の妥当性受理であり実転送成功そのものではないため、ACK 後に readiness と probe で補完する。

**Scale/Scope**: 1 team、3 server、3 region、10 country、4 public host patterns、1 Envoy node。
本番 VPN、TLS、複数 team、benchmark/scoring は対象外。

## Constitution Check

*GATE: Phase 0 前および Phase 1 後に確認済み。必須違反なし。*

- **Specification stage**: 支援インフラ仕様。成果物は Router Controller、Envoy 設定、3区画 Compose topology、
  自動 E2E、設計・運用文書。後続競技仕様へ Portal contract、国表、地理制約、検証済み版を引き渡す。
- **Competition correctness**: 公開契約は `proto/portal/v1/portal.proto` と Host／`X-Country-Code` のデータ面契約。
  TeamKey、3 server/region、10 country、Router-only path、last-known-good、latest-wins を不変条件とし、unit、contract、
  integration、E2E で同じ判定を行う。
- **Stage-appropriate quality boundary**: TeamKey と Config を境界で検証し、Router は秘密を持たず、任意 body を
  log しない。構造化 apply 診断、health/readiness、失敗報告、再接続、冪等性、clean E2E を今回必須とする。
  TLS/WAF/HA/長期保存/ホスト配備は明示的に後続へ委譲する。
- **Competition-only gates**: 負荷、採点、意図的ボトルネック、private registry、較正、試し解きは本仕様の対象外。
  後続競技仕様は本 feature の契約・E2E・version を pin してから定義する。
- **Quality and information separation**: pure builder の unit test、Portal contract test、xDS ACK/NACK integration、
  Compose E2E、失敗時 log 保存、専用 project cleanup を用意する。競技の想定解やボトルネック情報は含めない。

## Phase 0: Research Decisions

詳細は [research.md](./research.md) を参照。

1. `~/ad-private` の reconcile loop、interface の冪等化、nftables、go-control-plane xDS server の境界を踏襲し、
   WireGuard／参加者 firewall／repo proxy／PCAP は移植しない。
2. Controller と Envoy は Compose の `network_mode: service:router` で同一 network namespace を共有する。
3. server1〜3 は各1つの専用 internal bridge にだけ属し、peer subnet への明示 route は対応 Router IP を next hop にする。
4. server 間 shaping は Router egress の HTB class + netem。送信 server IP を source filter とし、destination server の
   region との同異で class を選ぶ。
5. HTTP shaping は Envoy の route-level fault と bandwidth-limit filter。Host×国の route matrix は RDS で更新する。
6. Envoy cluster/listener は固定、変動する route だけを ADS/RDS で配信し、構成依存順序と ACK 判定を単純化する。
7. nftables ruleset は単一 batch で置換し、tc は全 interface 適用失敗時に last-known-good を再適用する。
8. Controller は容量1の latest-wins queue、snapshot ごとの bounded retry、Portal stream の指数 backoff を使う。
9. E2E は production 値と同じ分類で delay/rate だけ縮尺変更し、中央値と転送時間に許容幅を持たせる。

未解決の `NEEDS CLARIFICATION` はない。

## Phase 1: Design and Contracts

- **Data model**: [data-model.md](./data-model.md)
- **Portal / HTTP / health contracts**: [contracts/README.md](./contracts/README.md)
- **Validation guide**: [quickstart.md](./quickstart.md)
- **Existing Protobuf SoT**: `proto/portal/v1/portal.proto`

Phase 1 後の Constitution Check でも必須違反はない。権限、契約、正しさ、診断、テスト、対象外が具体的な
source/test path に割り当てられ、競技情報は導入していない。

## Project Structure

### Documentation (this feature)

```text
specs/003-router/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── README.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
router/
├── go.mod
├── go.sum
├── Dockerfile
├── README.md
├── compose.e2e.yaml
├── envoy/
│   └── bootstrap.yaml
├── cmd/router/
│   └── main.go
├── internal/
│   ├── config/          # environment validation and defaults
│   ├── domain/          # config/snapshot validation and country mapping
│   ├── netcontrol/      # interface discovery, nft/tc render and transactional reconcile
│   ├── xds/             # RDS builder, snapshot cache/server, ACK/NACK tracker
│   ├── reconcile/       # Portal stream, latest-wins apply loop, reporting/retry
│   └── status/          # health/readiness and active snapshot diagnostics
├── test-fixtures/server/
│   ├── Dockerfile
│   ├── entrypoint.sh
│   └── main.go
└── tests/e2e/
    ├── Dockerfile
    ├── main.go
    └── run.sh
```

**Structure Decision**: 本体 `backend/`、`frontend/` と Portal Mock を変更せず、特権境界を持つ Router を独立 Go
module にする。公開 protobuf は既存 root `proto/` / `gen/go` を再利用する。fixture と E2E は `router/` 内へ閉じ、
production binary に test endpoint や Docker API 権限を持ち込まない。

## Apply Sequence and Rollback

1. snapshot/team/config/country を pure validation する。
2. interface address から実 interface 名を解決し、nft/tc candidate を生成・事前検査する。
3. network candidate を適用し、失敗時は直前の network config を復元する。
4. RDS candidate を publish し、対象 version の ACK を timeout 付きで待つ。
5. NACK/timeout 時は直前 RDS と network config を復元し、失敗報告する。
6. ACK 後に active snapshot/readiness を一度だけ更新し、Portal へ成功報告する。
7. 報告通信だけ失敗した場合は data plane を巻き戻さず、同じ成功報告を再送する。

## Complexity Tracking

Constitution Check の必須ゲート違反はない。例外・期限付き免除は不要。
