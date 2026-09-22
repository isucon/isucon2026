# Quickstart Validation Guide: ISUCON2026 Portal Mock

**Feature**: `002-portal-mock`
**Date**: 2026-07-26

実装完了後に Protobuf + ConnectRPC + Buf を含む主要要件を end-to-end で検証する。
論理モデルは `data-model.md`、共有 RPC 契約は `proto/portal/v1/portal.proto`、
モック固有契約は `proto/portal_mock/v1/portal_mock.proto`、生成・stream/error 規約は
`proto/README.md` と `contracts/README.md` を参照する。

## 1. Prerequisites

- Go 1.26.5 以上
- Node.js 24.14.1 / npm 11.11.0
- mise
- `jq`
- E2E 実行時は Playwright 対応ブラウザ

Buf CLI、protoc、コード生成プラグイン、Go、Nodeはリポジトリ直下の`mise.toml`で固定する。

```bash
mise install
```

想定配置:

```text
proto/portal/v1/portal.proto
proto/portal_mock/v1/portal_mock.proto
buf.yaml
buf.gen.yaml
gen/go/  gen/ts/
portal-mock/server/
portal-mock/web/
```

## 2. Contract validation and generation

リポジトリルートから共有 proto を検証・生成:

```bash
mise exec -- buf lint
mise exec -- buf generate
git diff --exit-code -- gen/go gen/ts

BASE_REF="${BASE_REF:-origin/main}"
BASE_DIR="$(mktemp -d)"
git archive "$BASE_REF" proto | tar -x -C "$BASE_DIR"
mise exec -- buf breaking --against "$BASE_DIR/proto"
rm -rf "$BASE_DIR"
```

buf 無しでの素の compile 確認:

```bash
mise exec -- protoc \
  -I proto \
  --descriptor_set_out=/tmp/isucon2026-proto.pb \
  proto/portal/v1/portal.proto \
  proto/portal_mock/v1/portal_mock.proto
```

**Expected**

- Protobuf lint/compile と breaking check が成功する。
- mise で固定したローカル plugin で共有 `gen/go` / `gen/ts` に Go message/Connect client-handler と
  TypeScript descriptor が生成される。
- BSR remote plugin／remote moduleへのアクセスは発生しない。
- 生成を再実行しても差分が発生しない。

## 3. Build and test

```bash
go -C portal-mock/server test ./...
npm --prefix portal-mock/web ci
npm --prefix portal-mock/web run lint
npm --prefix portal-mock/web run typecheck
npm --prefix portal-mock/web test
npm --prefix portal-mock/web run build
```

**Expected**

- Go の unit/Connect contract/integration/concurrency test が成功する。
- React の lint/typecheck/Vitest/build が成功する。

## 4. Start in default stand-in mode

Terminal A:

```bash
PORTAL_TEAM_NUMBER=1 \
PORTAL_TEAM_HASH=abc123 \
go -C portal-mock/server run ./cmd/portal
```

未指定時は `PORTAL_ROUTER_MODE=stand-in`、
`PORTAL_STANDIN_PROFILE=success`。

Terminal B:

```bash
npm --prefix portal-mock/web run dev -- --host 0.0.0.0
```

ブラウザで Vite が表示した URL を開く。

**Expected UI**

- 参照デザインと同じダークテーマ、ヘッダー、スコアカード、ベンチパネル、
  実行履歴、設定カード、モック注記。
- Team 1、server1/2/3=AP、メイン=server1、デフォルト国=JP。
- 最高/直近スコア未設定、実行回数0、履歴空。
- stand-in success のため desired=applied、反映待ちなし。
- 初期対応国（10か国）の国コードとオンデマンドの国名/リージョン一覧。

## 5. Buf curl helper

以下はリポジトリルートから実行する。

```bash
PORTAL_SCHEMA=proto
ROUTER_RPC=http://localhost:8080/portal.v1.PortalRouterService
MOCK_RPC=http://localhost:8080/portal_mock.v1.PortalMockService
TEAM='{"teamNumber":1}'

# Router向け portal/v1
router_rpc() {
  buf curl --protocol connect --schema "$PORTAL_SCHEMA" \
    --data "${2:-{}}" "$ROUTER_RPC/$1"
}

# Portal UI向け portal_mock/v1
mock_rpc() {
  buf curl --protocol connect --schema "$PORTAL_SCHEMA" \
    --data "${2:-{}}" "$MOCK_RPC/$1"
}
```

`--schema proto` はリポジトリ直下の共有 proto ディレクトリを指す。

## 6. Verify initial state

モックの総合状態（runtime + 共有 portal + シミュレーション）:

```bash
mock_rpc GetState '{}' | jq '.state | {
  mode: .runtime.routerMode,
  team: .portal.team,
  desiredSnapshot: .portal.desired.snapshotId,
  config: .portal.desired.config,
  calculated: .portal.desired.calculated,
  applied: .portal.applied,
  converged: .portal.converged,
  benchmark,
  scores,
  history
}'
```

**Expected**

- `mode == "ROUTER_MODE_STAND_IN"`
- `state.portal.team` は `teamNumber` だけを持ち、random-hash を含まない。
- server1/2/3 は `REGION_AP`
- mainServer は `SERVER_ID_SERVER1`
- defaultCountry は `"JP"`（string）
- applied.snapshot.snapshotId==desired.snapshotId、converged=true
- desired.calculated.hostnames は起動時に指定した `abc123` を含むホスト名。
- benchmark は IDLE / progress 0
- `scores.hasScores=false`, runsCount=0、history 空

国一覧:

```bash
mock_rpc ListSupportedCountries '{}' | jq '.countries'
```

**Expected**: 初期データは US/CA/MX、JP/KR/CN/AU、GB/DE/FR の10件。
各 `code` は `"US"` のような alpha-2 string。

## 7. Verify StreamDesired and convergence

Terminal C:

```bash
router_rpc StreamDesired "{\"team\":$TEAM}"
```

Terminal D:

```bash
mock_rpc UpdateDesired '{
  "patch": {
    "server2Region": "REGION_EU",
    "mainServer": "SERVER_ID_SERVER3",
    "defaultCountry": "US"
  }
}' | jq
```

**Expected**

1. `StreamDesired` の最初の message は接続時の最新 desired。
2. 更新後に新しい snapshotId とConfigを持つdesiredを受信する。CalculatedConfigは含まれない。
3. stand-in 報告前は desired/applied を分離し pending 表示。
4. success profile の `ReportApplied` 後、同 snapshotId/Config で converged=true。
5. `mock_rpc GetState`では、target hostnameがserver3、defaultCountryRegionがUSになっている。

## 8. Verify invalid input rejection

サポート対象外の alpha-2:

```bash
mock_rpc UpdateDesired '{
  "patch": {"defaultCountry": "XX"}
}'
```

リージョン名を国コードに誤用:

```bash
mock_rpc UpdateDesired '{
  "patch": {"defaultCountry": "AP"}
}'
```

形式不正（小文字・長さ違い）:

```bash
mock_rpc UpdateDesired '{
  "patch": {"defaultCountry": "jp"}
}'
mock_rpc UpdateDesired '{
  "patch": {"defaultCountry": "JPN"}
}'
```

**Expected**

- Connect code `INVALID_ARGUMENT`。
- desired の snapshotId/Config と applied は変化しない。
- `AP` / `EU` はリージョン名であり国コードとして受理しない。
- サーバーは `^[A-Z]{2}$` と現在の SupportedCountry データに対して国コードを検証する。
- 国コードは enum ではないため、対応国追加は SupportedCountry データ更新だけで可能
  （proto 変更・再生成を要しない）。

## 8.1 Verify TeamKey rejection

Router向けRPCに無効または対象外のTeamKeyを指定する:

```bash
router_rpc StreamDesired '{"team":{"teamNumber":0}}'
router_rpc StreamDesired '{"team":{"teamNumber":2}}'

router_rpc ReportApplied '{
  "team": {"teamNumber": 0},
  "failure": {"snapshotId": "any", "reason": "must not be accepted"}
}'
router_rpc ReportApplied '{
  "team": {"teamNumber": 2},
  "failure": {"snapshotId": "any", "reason": "must not be accepted"}
}'
```

**Expected**

- `teamNumber=0`は`INVALID_ARGUMENT`。
- 起動時のTeam 1と異なる正の`teamNumber=2`は`NOT_FOUND`。
- StreamDesiredは購読を開始せず、ReportAppliedは状態を変更しない。

## 8.2 Verify idempotent UpdateDesired

現在のdesiredを取得し、同じConfigになるpatchを送る:

```bash
BEFORE="$(mock_rpc GetState '{}')"
BEFORE_SNAPSHOT="$(printf '%s' "$BEFORE" | jq -r '.state.portal.desired.snapshotId')"

mock_rpc UpdateDesired '{
  "patch": {
    "server1Region": "REGION_AP",
    "server2Region": "REGION_AP",
    "server3Region": "REGION_AP",
    "mainServer": "SERVER_ID_SERVER1",
    "defaultCountry": "JP"
  }
}' | tee /tmp/noop-update.json | jq

AFTER_SNAPSHOT="$(mock_rpc GetState '{}' | jq -r '.state.portal.desired.snapshotId')"
test "$(jq -r '.changed' /tmp/noop-update.json)" = "false"
test "$BEFORE_SNAPSHOT" = "$AFTER_SNAPSHOT"
```

**Expected**

- 正常応答で`changed=false`。
- snapshot ID、desired、ApplyDiagnostic、pending開始時刻は変化しない。
- 接続中の`StreamDesired`／`StreamState`には新しいmessageが流れない。
- 自動contract/integration testでは、両streamのイベント発行件数が0であることも検証する。

## 9. Verify benchmark and lock

収束状態で:

```bash
mock_rpc StartBenchmark '{}' | jq
```

実行中に:

```bash
mock_rpc UpdateDesired '{
  "patch": {
    "mainServer": "SERVER_ID_SERVER1"
  }
}'
```

進捗:

```bash
mock_rpc StreamState '{}'
```

または:

```bash
watch -n 0.2 "buf curl --protocol connect --schema '$PORTAL_SCHEMA' \
  --data '{}' '$MOCK_RPC/GetState' | jq '.state | {locked: .portal.locked, benchmark, scores, history}'"
```

**Expected**

- 状態が PENDING → RUNNING と遷移し progress が増える。
- 実行中の UpdateDesired は `FAILED_PRECONDITION`。
- 完了後 IDLE/unlocked、履歴先頭へ1件追加。
- latest/best/runsCount 更新、UI でログ展開可能。
- ベンチマークは UI シミュレーションであり、実ベンチマーカーとは通信しない。

## 10. Verify pending blocks benchmark

`no-report` profile でサーバーを起動:

```bash
PORTAL_ROUTER_MODE=stand-in \
PORTAL_STANDIN_PROFILE=no-report \
PORTAL_TEAM_NUMBER=1 \
PORTAL_TEAM_HASH=abc123 \
go -C portal-mock/server run ./cmd/portal
```

```bash
mock_rpc UpdateDesired '{
  "patch": {
    "server2Region": "REGION_EU"
  }
}' | jq

mock_rpc StartBenchmark '{}'
```

**Expected**

- desired 更新後も applied は従来値。
- 時間経過後も desired は revert されず DELAYED。
- StartBenchmark は `FAILED_PRECONDITION`。

## 11. Verify fail-once retry

`PORTAL_STANDIN_PROFILE=fail-once-then-success` で起動し設定を1回変更。

**Expected**

1. 同 snapshotId の初回報告は失敗、applied は従来値。
2. ApplyState は FAILED_RETRYABLE、reason を表示。
3. Portal は新しい desired スナップショットを自動発行しない。
4. 同 snapshotId の再試行成功後 CONVERGED。

## 12. Verify external-router mode

```bash
PORTAL_ROUTER_MODE=external-router \
PORTAL_TEAM_NUMBER=1 \
PORTAL_TEAM_HASH=abc123 \
go -C portal-mock/server run ./cmd/portal
```

```bash
mock_rpc GetState '{}' | jq '.state | {
  mode: .runtime.routerMode,
  desired: .portal.desired,
  applied: .portal.applied,
  apply: .portal.apply,
  converged: .portal.converged
}'
```

**Expected**

- mode は EXTERNAL_ROUTER。
- desired は既知初期値。
- applied.snapshot は未設定、ApplyState=UNSYNCED、converged=false。
- 内蔵 stand-in は起動しない。

生成 Go client または `buf curl` の `StreamDesired` で受信した
snapshotId と Config を `ReportApplied` へ渡す:

```bash
router_rpc ReportApplied @/tmp/report-applied.json | jq
```

`router_rpc` helper がファイル入力を扱わない場合は直接:

```bash
buf curl \
  --protocol connect \
  --schema "$PORTAL_SCHEMA" \
  --data @/tmp/report-applied.json \
  "$ROUTER_RPC/ReportApplied"
```

`/tmp/report-applied.json` は次の形:

```json
{
  "team": {"teamNumber": 1},
  "success": {
    "snapshotId": "<StreamDesiredから受信>",
    "applied": {
      "...": "Routerが実際に適用しているConfig"
    }
  }
}
```

**Expected**: Configが妥当ならresponseは空。報告したsnapshotId/Configがappliedへ反映され、
desiredと一致すればconverged=true。

## 13. Verify Router report semantics

次の成功報告を送る:

- Portal が発行していない snapshotId。
- desired と異なる Config。

**Expected**

- RPC transport は成功。
- responseは空。
- Configが妥当であれば、snapshotIdの既知性やdesiredとのConfig一致にかかわらず、
  報告した値がappliedへ反映される。
- desiredと一致しないためconverged=falseとなり、反映待ちを継続する。

以前に発行されたsnapshotIdへの「後退」報告でも、appliedがその値へ更新されることを
併せて確認する（applied の SoT は Router）。

構造・必須項目・値域が不正なConfigを成功報告する:

**Expected**

- Connect codeは`INVALID_ARGUMENT`。
- AppliedConfigは変化しない。
- ApplyStateは`INVALID_REPORT`となり、reasonに妥当性エラーが入る。
- 後続の妥当な成功報告を受理するとreasonをクリアし、収束時は`CONVERGED`、
  未収束時は経過時間に応じて`PENDING`または`DELAYED`となる。

失敗報告:

```json
{
  "team": {"teamNumber": 1},
  "failure": {
    "snapshotId": "<対象のsnapshotId>",
    "reason": "router apply failed"
  }
}
```

**Expected**: appliedは変化しない。現在desiredに対する失敗ならApplyStateがFAILED_RETRYABLEとなり、
理由が表示される。

## 14. Verify reset priority and determinism

```bash
OLD_SNAPSHOT="$(mock_rpc GetState '{}' | jq -r '.state.portal.desired.snapshotId')"
OLD_APP_HOST="$(mock_rpc GetState '{}' | jq -r '.state.portal.desired.calculated.hostnames.app')"
mock_rpc Reset '{}' | tee /tmp/reset-state.json | jq
NEW_SNAPSHOT="$(jq -r '.state.portal.desired.snapshotId' /tmp/reset-state.json)"
NEW_APP_HOST="$(jq -r '.state.portal.desired.calculated.hostnames.app' /tmp/reset-state.json)"
test "$OLD_SNAPSHOT" != "$NEW_SNAPSHOT"
test "$OLD_APP_HOST" = "$NEW_APP_HOST"
```

**Expected**

- StreamDesired/StreamState は接続を維持し、新しい初期状態を受信する。
- ベンチ/stand-in 処理 cancel。
- 初期設定、IDLE、進捗0、空履歴、未設定スコアへ即時復帰。
- reset 前の遅延完了が後から状態を更新しない。
- `OLD_SNAPSHOT` を含む成功報告もappliedへ反映され、新しいdesiredへの反映待ちとなる。
- snapshot ID は変わるが、TeamHostnameConfig は維持されるためホスト名は変わらない。
- stand-in は desired=applied、external-router は applied 不明。

決定的ベンチ列:

1. reset 後に複数回実行し、result/score/logs を保存。
2. 再度 reset。
3. 同じ回数実行して比較。

**Expected**: PASS と FAIL の双方を含み、2回の列が完全一致。

## 15. E2E suite

```bash
npm --prefix portal-mock/web run test:e2e
```

最低限:

- Connect-ES で設定変更 → pending → Router報告 → applied確定。
- desired/applied と UI/共有 GetState の一致。
- 初期10か国の受理と、形式不正・対象外・リージョン名の国コード拒否。
- pending 中のベンチ拒否。
- 実行中ロック、StreamState 進捗、履歴/スコア更新。
- reset と旧 snapshot ID 報告・非同期処理の無効化。
- 同一 seed の結果列再現。
