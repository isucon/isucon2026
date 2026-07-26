# Portal Mock Protobuf

## Router向け定義

`proto/portal/v1/portal.proto`

PortalとRouterの通信に使用する。

`TeamKey`はチーム番号だけを持つ。`StreamDesired`が返す`ConfigSnapshot`には、
Routerが適用する`Config`とsnapshot IDだけを含める。

| RPC | Type | Consumer |
|---|---|---|
| `PortalRouterService.StreamDesired` | Server streaming | Router |
| `PortalRouterService.ReportApplied` | Unary | Router / stand-in |

実ベンチマーカーとの通信 RPC は現時点では定義しない。
国コードは enum ではなく ISO 3166-1 alpha-2 の `string` とし、
PortalMockServiceの`ListSupportedCountries`が現在の対応国データを返す。

## モックポータル向け定義

`proto/portal_mock/v1/portal_mock.proto`

| RPC | Type | Consumer |
|---|---|---|
| `PortalMockService.GetState` | Unary | Mock UI / CI |
| `PortalMockService.StreamState` | Server streaming | Mock UI |
| `PortalMockService.ListSupportedCountries` | Unary | Mock UI |
| `PortalMockService.UpdateDesired` | Unary | Mock UI |
| `PortalMockService.StartBenchmark` | Unary | Mock UI |
| `PortalMockService.Reset` | Unary | Mock UI / CI |

`portal_mock/v1`は`portal/v1`をimportし、Region、ServerId、TeamKey、Configを再利用する。
PortalState、CalculatedConfig、AppliedConfig、ApplyDiagnostic、SupportedCountryは
モックポータル向けのため`portal_mock/v1`に定義する。

## Connect error policy

| Situation | Connect code / response |
|---|---|
| enum UNSPECIFIED、国コード形式不正/対象外、空 patch、ReportAppliedのoneof欠落、不正な成功報告 | `INVALID_ARGUMENT` |
| team_number=0 | `INVALID_ARGUMENT` |
| 起動時設定と異なる正の TeamKey | `NOT_FOUND` |
| ベンチ実行中の設定変更 | `FAILED_PRECONDITION` |
| 未収束・applied 不明・実行中のベンチ開始 | `FAILED_PRECONDITION` |
| クライアントによる stream 切断 | `CANCELED` |
| 想定外の内部エラー | `INTERNAL` |

Router向けRPCのTeamKeyは、`team_number=0`を`INVALID_ARGUMENT`、起動時に設定したチームと
異なる正の値を`NOT_FOUND`として区別する。どちらも状態を変更せず、`StreamDesired`では
購読を開始しない。

## StreamDesired semantics

1. 接続直後に対象 TeamKey の最新 `ConfigSnapshot` を必ず 1 件返す。
2. desired 変更時に最新スナップショットを返す。
3. 読み取りが遅い場合、未送信の中間スナップショットは最新値へ coalesce できる。
4. `PortalMockService.Reset` 後も stream を維持し、新しい初期スナップショットを返す。
5. Router は再接続時の最初の message から最新 desired を取得する。
6. Router は反映後、TeamKey、適用対象のsnapshotId、実際のConfigを成功報告する。
7. Portal はsnapshotIdの既知性やConfigのdesiredとの一致で成功報告を拒否せず、
   最後の成功報告をappliedとする。

## ReportApplied semantics

- 成功報告は`ApplySuccess`、失敗報告は`ApplyFailure`で表す。
- 構造・必須項目・値域が妥当な成功報告はAppliedConfigを更新する。
- 不正な成功報告は`INVALID_ARGUMENT`を返し、AppliedConfigを変更せず、
  診断状態を`INVALID_REPORT`として理由を記録する。
- `INVALID_REPORT`後に妥当な成功報告を受理した場合は妥当性エラーの理由を消去し、
  desiredとappliedが一致すれば`CONVERGED`、一致しなければpending経過時間に応じて
  `PENDING`または`DELAYED`へ遷移する。
- 失敗報告はAppliedConfigを更新しない。現在desiredに対する失敗の場合だけ失敗診断を更新する。
- `ReportAppliedResponse`は空。受理結果のenumは返さない。

## UpdateDesired semantics

1. 空patch、UNSPECIFIED enum、形式不正または対象外の国コードは`INVALID_ARGUMENT`とする。
2. ベンチマークのpending/running中は`FAILED_PRECONDITION`とする。
3. 有効なpatch適用後のConfigが現在のdesiredと異なる場合だけ、新しいsnapshot IDを発行し、
   `changed=true`として`StreamDesired`／`StreamState`へ最新状態を配信する。
4. 適用後のConfigが現在のdesiredと完全一致する場合は正常なno-opとし、`changed=false`と
   現在のdesired／診断を返す。snapshot ID、desired、診断、pending開始時刻を変更せず、
   両streamへ更新を配信しない。

## StreamState semantics

1. 接続直後に現在の`MockPortalState`を必ず1件返す。
2. desired/applied/診断/ベンチ進捗/履歴/reset の変化時に最新状態を返す。
3. 中間状態は coalesce 可能。

## Build and generation

ルートの `buf.yaml` / `buf.gen.yaml` を利用する。

```bash
mise install
mise exec -- buf lint
mise exec -- buf generate

BASE_REF="${BASE_REF:-origin/main}"
BASE_DIR="$(mktemp -d)"
git archive "$BASE_REF" proto | tar -x -C "$BASE_DIR"
mise exec -- buf breaking --against "$BASE_DIR/proto"
rm -rf "$BASE_DIR"
```

`buf.gen.yaml` は mise が提供するローカル `protoc-gen-*` のみを使用し、BSR remote plugin／
remote module は使用しない。生成先は共有 `gen/go/` と `gen/ts/`。生成コードは手編集しない。
