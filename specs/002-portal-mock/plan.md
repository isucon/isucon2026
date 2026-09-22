# Implementation Plan: ISUCON2026 開発用モックポータル（1チーム前提）

**Branch**: `002-portal-mock` | **Date**: 2026-07-26 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/002-portal-mock/spec.md`

## Summary

`docs/geometrics-distribution.md` の開発・動作確認用に、ISUReal 本体から独立した
1チーム専用モックポータルを新設する。参照デザインを React/TypeScript で再現し、
Go + ConnectRPC のメモリ内サーバーが snapshot ID 付き desired を Single Source of Truth として保持する。
Router 起点の Connect server-streaming RPC `StreamDesired` で最新 desired を配信し、
Router/内蔵 stand-in が unary RPC `ReportApplied` で成功報告した applied だけを
確定値として公開する。Portal・Router 間の公開契約は Protobuf を正とし、
リポジトリ直下 `/proto` を Single Source of Truth として `portal/v1`（本番共有）と
`portal_mock/v1`（モック固有）に分割する。Buf で lint・breaking check・共有
Go/TypeScript コード生成を行うが、BSR remote plugin／remote module は使用しない。
Buf CLI、protoc、各ローカル生成プラグインはリポジトリ直下の `mise.toml` で固定する。
実ベンチマーカーとの通信契約は本フェーズでは作成せず、
UI 上のベンチマークシミュレーションだけを `portal_mock/v1` に置く。

実装は `portal-mock/server/` と `portal-mock/web/` の独立モジュールとし、本体の認証、
画面、API、compose ルーティングへ組み込まない。再利用しない snapshot ID、desired スナップショット、
固定シードのベンチシミュレーションにより、反映待ち・失敗/遅延・実行中ロック・履歴更新を
CI で決定的に検証できるようにする。

## Technical Context

**Language/Version**: Go 1.26.5、TypeScript 5.9.3

**Primary Dependencies**: Connect-Go、Protobuf-Go、Connect-ES、Protobuf-ES、
Buf CLI、protoc、ローカル protoc plugin、mise、React 19.2、Vite 8.1、
Go `net/http` / `context`

**Storage**: プロセスメモリのみ。永続化・DB・マイグレーションなし

**Testing**: `buf lint` / `buf breaking`、`go test`（unit/Connect contract/integration/concurrency）、
Vitest、Playwright、生成差分検査

**Target Platform**: Linux 開発環境上の独立 HTTP サーバー + モダンブラウザ

**Project Type**: 独立 Web application（Go server + React SPA）

**Performance Goals**: 1チーム・3サーバー・単一 Router 接続の開発用途で、意図的な
シミュレーション遅延を除く状態 RPC/UI 操作をローカル環境で即応させる。
Connect server-streaming は接続時と更新時に最新 desired へ確実に収束させる。
競技負荷性能・スコア性能は対象外

**Constraints**: 認証なし、1チーム、メモリ保持、desired/applied の SoT 分離、
reset/再起動で決定的初期化、Routerの成功報告は相関上の差では拒否せず構造・値域不正時だけ拒否、
実 Router と実ベンチ通信なし、
stand-in/external-router は起動時排他、国コードは拡張可能な ISO alpha-2 string、
Protobuf package v1 互換性、生成コードを手編集しない、BSR remote機能を使用しない、
ツール版を `mise.toml` で固定する、UI は日本語

**Scale/Scope**: 単一ポータル画面、1チーム、server1〜3、US/AP/EU、固定10か国、
Router購読1接続を主対象、ベンチ履歴はプロセス稼働中のみ

## Constitution Check

*GATE: Phase 0 開始前に評価し、Phase 1 設計後に再評価する。*

### Pre-Design Gate

- **Specification stage — PASS**:
  本仕様自体の宣言は「支援ツール仕様（アプリ基準仕様・競技仕様のいずれにも属さない独立成果物）」。
  Constitution のゲート適用区分は **application baseline 相当**を採用し、公開契約、入力検証、
  状態不変条件、初期化、テスト、後続引渡しを必須とする。競技負荷・採点・ボトルネック・想定解は
  本成果物に含めず、実 Router/実ベンチ連携時に契約版を固定して後続仕様へ引き渡す。
- **Competition correctness — PASS**:
  Router向け定義は Protobuf package `portal.v1` の `PortalRouterService`
  （`StreamDesired`、`ReportApplied`）と、モック固有
  `portal_mock.v1` の `PortalMockService`
  （`GetState`/`StreamState`/`ListSupportedCountries`/`UpdateDesired`/
  `StartBenchmark`/`Reset`）。
  主要不変条件は (1) desired SoT=Portal、applied SoT=Router、
  (2) Router 未報告値を applied にしない、(3) applied は Router の最後の成功報告に忠実
  （snapshot ID の既知性・Config一致・順序を理由に拒否しない）、
  (4) 実行中設定ロック、(5) 10か国のみ受理、(6) reset 後に旧非同期結果を破棄。
  Router向けRPCではteam_number=0と別チームを区別して拒否し、同値のdesired更新は
  snapshot ID・診断・streamイベントを変更しない冪等no-opとする。不正な成功報告後は
  妥当な成功報告でINVALID_REPORTの理由を消去し、収束状態を再計算する。
  競合ケースは設定変更対実行開始、reset 対ベンチ/stand-in/Connect stream、
  遅延・重複・中間スナップショット報告を含む。
- **Stage-appropriate quality boundary — PASS**:
  認証・認可は開発用公開モックとして仕様上不要。入力検証、desired/applied 分離、
  失敗/遅延診断、ロック拒否理由、契約/unit/integration/E2E/concurrency test は必須。
  永続化、レート制限、WAF、可用性目標、監査/長期ログ、実通信制御、実採点は対象外と明記済み。
- **Competition-only gates — PASS (downstream handoff)**:
  独立ボトルネック、非公開課題台帳、実負荷/採点、較正、試し解きは本支援ツールには導入しない。
  Portal–Router 契約 v1、国一覧、desired/applied 状態、初期化/決定的結果列を後続仕様へ引き渡す。
- **Quality and information separation — PASS**:
  Go unit/contract/integration/concurrency、React unit、Playwright E2E を CI で再現する。
  DB がないため migration handling は N/A。配布時は `portal-mock/` だけで独立ビルドできることを確認する。
  公開成果物に競技ボトルネック・想定解・非公開スコア情報を含めない。

### Post-Design Re-check

- **PASS — Contract completeness**:
  `proto/portal/v1/portal.proto`はRouter向けのdesired streamとapplied報告、
  およびそこから参照される最小限の型だけを定義した。
  `proto/portal_mock/v1/portal_mock.proto`はUI向け状態取得・設定変更・国一覧と、
  calculated/applied/diagnostic、reset/runtime/ベンチシミュレーションを定義した。
  実ベンチマーカーとの通信契約は意図的に未定義。
  `proto/README.md` と `contracts/README.md` が
  接続時最新値、coalesce、reset 後の初期スナップショット配信、再接続収束、Connect error policy、
  Protobuf versioning、TeamKey検証、同値更新のno-opとstream非配信、
  INVALID_REPORT後の診断回復を定義した。
- **PASS — Data invariants and concurrency**:
  `data-model.md` に TeamKey、Config、Router向け/UI向けConfigSnapshot、CalculatedConfig、
  AppliedConfig、ApplyReport、DesiredSubscription、BenchmarkState/Run、PortalStateの
  検証・遷移・cross-entity invariantsを定義した。
- **PASS — Initialization and reproducibility**:
  stand-in/external-router 別の既知初期状態、reset 優先、固定シード結果列を
  `data-model.md` と `quickstart.md` に反映した。
- **PASS — Validation coverage**:
  `quickstart.md` が主要フロー、無効国、pending/lock、external-router、
  Router報告、reset/snapshot ID、決定的ベンチ列の実行可能な検証シナリオを示す。
- **PASS — Information boundary**:
  本設計は実負荷、採点式、ボトルネック台帳、想定解、複数チーム、永続化を含まない。

## Phase 0: Research Decisions

詳細は [research.md](./research.md) を参照。

1. Go + ConnectRPC / Protobuf / Buf と React/TypeScript/Vite を、
   独立 `portal-mock/` モジュールとして採用。
2. Streaming Pull は Connect server-streaming `StreamDesired`、
   applied は unary `ReportApplied`。
3. TeamKey は teamNumber のみにし、random-hash はホスト名生成用の TeamHostnameConfig として分離する。
4. Router の成功報告を snapshotId/Config の一致では拒否せず、構造・値域が妥当なら
   報告されたConfigをappliedへ反映する。不正な成功報告は`INVALID_ARGUMENT`として
   AppliedConfigを維持し、診断状態を`INVALID_REPORT`にする。
   成功/失敗はrequestの`oneof`で表し、response message自体は空とする。
5. stand-in は external Router と同じ検証経路を使用し、3つの決定的 profile を提供。
6. reset は最優先で処理を cancel、PRNG・状態を初期化して新しいsnapshot IDを発行し、
   接続中の stream へ新しい初期スナップショットを配信する。
7. ベンチ結果は固定シードで PASS/FAIL・スコア・ログを再現。
8. 国コードは enum ではなく ISO alpha-2 string。初期データは10か国とし、
   対象外コードと AP/EU の国指定を拒否するが、国追加は proto 変更なしで可能。
9. 参照デザインの視覚構成を維持し、挙動を desired/applied モデルへ置換。
10. Bufbuild の BSR remote plugin／remote module は使用せず、Buf CLI・protoc・
    `protoc-gen-go`・`protoc-gen-connect-go`・`protoc-gen-es` を `mise.toml` で固定する。
    breaking check はローカルに展開した Git ref の `proto/` を比較元とする。
11. 同値の `UpdateDesired` は `changed=false` のno-opとし、snapshot ID、診断、
    `StreamDesired`／`StreamState` の配信を一切変更しない。
12. Router向けTeamKeyの入力エラーと対象不在を区別し、不正報告後の診断は後続の妥当な成功報告で
    現在のdesired/appliedに基づき回復させる。

未解決の `NEEDS CLARIFICATION` はない。

## Phase 1: Design and Contracts

- **Data model**: [data-model.md](./data-model.md)
- **Shared Protobuf contract** (repo root): `proto/portal/v1/portal.proto`
- **Mock-only Protobuf contract** (repo root): `proto/portal_mock/v1/portal_mock.proto`
- **Buf configuration** (repo root): `buf.yaml` / `buf.gen.yaml`
- **Contract SoT overview** (repo root): `proto/README.md`
- **Contract mapping** (feature): [contracts/README.md](./contracts/README.md)
- **Validation guide**: [quickstart.md](./quickstart.md)

## Project Structure

### Documentation (this feature)

```text
specs/002-portal-mock/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── README.md            # /proto への contract mapping（proto は複製しない）
├── design/
│   ├── ISUCON Portal Dashboard.dc.html
│   ├── support.js
│   ├── thumbnail.webp
│   └── README.md
└── tasks.md                 # /speckit-tasks で作成。plan では作成しない
```

### Source Code (repository root)

```text
proto/                        # リポジトリ全体で共有する Protobuf source（SoT）
├── README.md
├── portal/
│   └── v1/
│       └── portal.proto      # 本番共有: Portal/Router 契約
└── portal_mock/
    └── v1/
        └── portal_mock.proto # モック固有: reset/runtime（portal/v1 を import）

buf.yaml                      # workspace module = proto
buf.gen.yaml                  # managed mode、mise管理のlocal pluginだけを使用
mise.toml                     # Buf CLI、protoc、生成plugin、Go、Nodeを固定

gen/                          # buf generate の共有出力（手編集禁止）
├── go/                       # protoc-gen-go / protoc-gen-connect-go
│   ├── portal/v1/
│   └── portal_mock/v1/
└── ts/                       # protoc-gen-es
    ├── portal/v1/
    └── portal_mock/v1/

portal-mock/
├── server/
│   ├── go.mod               # 共有 gen/go module を import
│   ├── cmd/
│   │   └── portal/
│   │       └── main.go
│   ├── internal/
│   │   ├── rpc/             # PortalRouterService/PortalMockService handler、error mapping
│   │   ├── config/          # 起動モード、team/hash、timeout
│   │   ├── domain/          # 値型・状態・不変条件
│   │   ├── store/           # 同期されたメモリ状態、チーム別スナップショット
│   │   ├── standin/         # success/fail-once/no-report
│   │   └── benchmark/       # 固定シード結果列、進捗、cancel
│   └── tests/
│       ├── contract/
│       ├── integration/
│       ├── concurrency/
│       └── unit/
├── web/
│   ├── package.json
│   ├── vite.config.ts
│   ├── playwright.config.ts
│   ├── src/
│   │   ├── rpc/             # Connect-ES transport/client（共有 gen/ts を import）
│   │   ├── app/             # Portal page、状態取得/更新
│   │   ├── components/      # score/benchmark/history/settings/country help
│   │   └── styles/          # 参照デザイン再現
│   └── tests/
│       ├── unit/
│       └── e2e/
├── README.md
└── compose.yaml             # portal-mock 単体用。本体 compose とは分離
```

**Structure Decision**: ISUReal 本体の `backend/` と `frontend/` へ機能を追加せず、
システム層の公開契約はリポジトリ直下 `/proto` を SoT とし、本番 Portal/Router が
共有する `portal/v1` と、モック固有の `portal_mock/v1` に分割する。生成コードは共有 `gen/`
へ出力し、`portal-mock/server` と `portal-mock/web` が import する（将来の Router も同様）。
ベンチマーカー契約は後続仕様で別途追加し、現時点では共有 proto に先取りしない。
モック実装（server/web/compose）は本体 `backend/`・`frontend/` へ組み込まず独立させる。
server の domain/store 境界に TeamKey/snapshotId とロック不変条件を集約し、
Connect handler/stand-in/benchmark が同じ状態遷移を利用する。

## Implementation Sequence

1. 共有 `/proto`（portal/v1・portal_mock/v1）、root Buf module、mise で固定したローカル生成 plugin、
   共有 `gen/` 出力、契約 CI を用意。
2. `mise install` 後、`mise exec -- buf generate` で Go/TypeScript を生成し、生成差分検査と
   ローカル Git ref を比較元とする `buf breaking` を設定。
3. domain 値型、10か国、TeamKey/TeamHostnameConfig/Config/CalculatedConfig、initial state を実装。
4. TeamKey/snapshotId 付きメモリ store と desired/applied 検証を実装し、
   同値更新no-op、TeamKeyエラー、INVALID_REPORT回復を含む unit/concurrency test。
5. PortalRouterService の unary/server-streaming handler と Connect contract test を実装。
6. PortalMockService（UI操作/reset/runtime）、stand-in 3 profile、reset cancel/新状態の stream 配信を
   実装し integration test。
7. 固定シード benchmark state machine、ロック、履歴/score 更新を実装し再現性 test。
8. 参照デザインを React で再現し、Connect-ES client、10か国補助 UI、
   desired/applied 診断を接続。
9. Playwright で quickstart の主要フローを自動化。
10. 独立 compose/README/CI command を整備し、本体へ混入しないことを確認。

## Complexity Tracking

Constitution Check の必須ゲート違反はない。例外・期限付き免除は不要。
