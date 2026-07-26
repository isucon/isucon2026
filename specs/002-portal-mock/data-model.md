# Data Model: ISUCON2026 開発用モックポータル

**Feature**: `002-portal-mock`
**Date**: 2026-07-26
**Source**: `specs/002-portal-mock/spec.md`

本成果物はプロセスメモリ上の論理データモデルを定義する。永続化・マイグレーションは対象外。
ワイヤ表現と RPC はリポジトリ直下の共有 proto
`proto/portal/v1/portal.proto`（本番共有）と `proto/portal_mock/v1/portal_mock.proto`
（モック固有）、生成・互換性規約は `proto/README.md` と `contracts/README.md` を参照する。
Protobuf の enum はすべて `UNSPECIFIED=0` を持ち、入力では UNSPECIFIED を拒否する。
共有契約 `portal/v1` はモック固有の mode/profile/reset を持たない。

## 1. 値オブジェクト

### Region

`US | AP | EU` の列挙値。サーバー配置および国の通信リージョンを表す。

**Validation**

- 大文字の列挙値だけを受理する。
- 国コードとは別概念。`AP` / `EU` をデフォルト国として受理しない。

### ServerID

`server1 | server2 | server3` の列挙値。

### CountryCode

ISO 3166-1 alpha-2 の大文字2文字を表す論理文字列型。Protobuf では enum にせず
`string` とする。

初期版で許可されるデータ:

`US | CA | MX | JP | KR | CN | AU | GB | DE | FR`

**Validation**

- 正規表現 `^[A-Z]{2}$` を満たす。
- 現在の SupportedCountry 一覧に存在する。
- 対応国追加は SupportedCountry データと仕様を更新し、proto schema は変更しない。

### TeamKey

| Field | Type | Rules |
|---|---|---|
| `teamNumber` | positive integer | チーム番号。0は`INVALID_ARGUMENT`、起動時設定と異なる正の値は`NOT_FOUND` |

### TeamHostnameConfig

Portal がホスト名生成に使用する内部設定。RPC の TeamKey には含めない。

| Field | Type | Rules |
|---|---|---|
| `randomHash` | string | チームのホスト名を推測しにくくする文字列。識別・認証には使用しない |
| `domainSuffix` | string | 既定 `isucon2026.westens.tokyo` |

**Derived hostnames**

- app: `app.team{teamNumber}-{randomHash}.{domainSuffix}`
- server: `server{1..3}.team{teamNumber}-{randomHash}.{domainSuffix}`

モックでは TeamHostnameConfig を起動時に設定し、reset では変更しない。このため reset 前後で
ホスト名は変化しない。

## 2. SupportedCountry

Portal と Router が共有する国定義。初期版は10件だが、Protobuf schema 上は件数を固定しない。

| Field | Type | Rules |
|---|---|---|
| `code` | CountryCode | 一意 |
| `nameJa` | string | 日本語表示名 |
| `region` | Region | 一国につき一つ |

**v1 records**

| code | nameJa | region |
|---|---|---|
| US | アメリカ合衆国 | US |
| CA | カナダ | US |
| MX | メキシコ | US |
| JP | 日本 | AP |
| KR | 大韓民国 | AP |
| CN | 中華人民共和国 | AP |
| AU | オーストラリア | AP |
| GB | イギリス | EU |
| DE | ドイツ | EU |
| FR | フランス | EU |

## 3. Config

ユーザーまたは Router が変更・適用する設定値。

| Field | Type | Rules |
|---|---|---|
| `serverRegions` | map(ServerID, Region) | server1〜3 を必ず各 1 件含む |
| `mainServer` | ServerID | server1〜3 のいずれか |
| `defaultCountry` | CountryCode | SupportedCountry に存在すること |

**Invariant**

- desired と applied の内容一致判定は Config の完全一致で行う。
- TeamKey と導出値は Config に含めない。

## 4. CalculatedConfig

TeamKey、TeamHostnameConfig、Config から計算する読み取り専用の値。

| Field | Type | Rules |
|---|---|---|
| `hostnames.app` | string | TeamKey と TeamHostnameConfig から導出 |
| `hostnames.servers` | map(ServerID, string) | TeamKey と TeamHostnameConfig から導出 |
| `hostnames.target` | string | `mainServer` に対応する server hostname |
| `defaultCountryRegion` | Region | SupportedCountry から導出 |

**Invariant**

- クライアントや Router から導出値を入力として受け取らない。
- ConfigSnapshot を返すときに Portal が計算する。

## 5. ConfigSnapshot

論理モデルでは、Routerへの配信値とモックUIの表示値を次のように区別する。

| Field | Type | Rules |
|---|---|---|
| `snapshotId` | opaque string | スナップショットごとに一意。順序を持たない |
| `config` | Config | 変更・適用対象 |
| `issuedAt` | timestamp | 診断用 |

- `portal.v1.ConfigSnapshot`: Router向け。上記3フィールドだけを持つ。
- `portal_mock.v1.ConfigSnapshot`: UI向け。上記に`calculated: CalculatedConfig`を加える。

**Snapshot lifecycle**

- 有効な設定変更 1 回につき、過去に再利用しないランダムな `snapshotId`（UUID 等）を発行する。
- 不正入力、実行中ロックによる拒否、現在値と同じ値の指定では新しいスナップショットを発行しない。
- 現在値と同じ値になる有効patchは`changed=false`を返し、desired、ApplyDiagnostic、
  pending開始時刻を変更せず、`StreamDesired`／`StreamState`へ更新を配信しない。
- 空patchは有効な同値指定ではなく入力不正として拒否する。
- `snapshotId` に順序はない。新旧の判定には使わない。
- reset 時は初期 Config に過去と異なる snapshotId を発行する。

## 6. AppliedConfig

Router 役が最後に成功報告し、構造・必須項目・値域の検証を通過した反映済み設定。
applied の SoT は Router なので、Portal は妥当な報告に忠実に従う。

| Field | Type | Rules |
|---|---|---|
| `snapshotId` | opaque string / null | Router が適用対象とした desired。未知のIDも保持する |
| `config` | Config / null | Router が実際に適用している設定 |
| `calculated` | CalculatedConfig / null | TeamHostnameConfig と報告された Config から Portal が計算 |
| `reportedAt` | timestamp / null | 成功報告の受信時刻 |

**Invariant**

- Portal が desired を直接コピーして更新してはならない。Router/stand-in の成功報告だけで更新する。
- snapshotId が未発行、Config が desired と不一致、以前のスナップショットへの後退であっても、
  Configが妥当ならRouter の実状態として applied を更新する。
- 構造・必須項目・値域が不正な成功報告はAppliedConfigを更新せず、`INVALID_ARGUMENT`を返す。
- CalculatedConfig は Router から受け取らず、報告された Config から Portal が再計算する。

## 7. ApplyReport / ApplyDiagnostic

Router 役からの反映成否報告と、Portal が保持する診断情報。

### ApplyReport

| Variant | Fields | Behavior |
|---|---|---|
| `success` | `snapshotId`, `applied: Config` | Configが妥当ならAppliedConfigを更新。不正なら`INVALID_ARGUMENT` |
| `failure` | `snapshotId`, `reason` | AppliedConfig は変更しない |

成功と失敗は `oneof` で表現する。成功報告には実際の Config が必要で、失敗報告には失敗理由が
必要という入力構造の違いを型で保証するためである。`ReportAppliedResponse` は空とし、受理結果の
enum は返さない。

不正な成功報告はRPCエラー`INVALID_ARGUMENT`とし、`ReportAppliedResponse`のフィールド追加は行わない。
このときAppliedConfigを維持し、診断状態を`invalid-report`、reasonを妥当性エラーとする。

失敗報告の snapshotId が現在 desired と一致する場合は `failed-retryable` と理由を表示する。
現在 desired と異なる失敗報告は現在の診断状態を変更しない。どちらも RPC 自体は正常終了する。

### ApplyDiagnostic

| Field | Type | Rules |
|---|---|---|
| `state` | `converged | pending | delayed | failed-retryable | unsynced | invalid-report` | 表示状態 |
| `desiredSnapshotId` | string | 現在 desired |
| `appliedSnapshotId` | string / null | 現在 applied |
| `reason` | string / null | 最後の失敗理由 |
| `pendingSince` | timestamp / null | 現 desired 発行時刻 |
| `delayed` | boolean | 診断しきい値超過 |

**Invariant**

- `failed-retryable` と `delayed` は終端状態ではない。
- `invalid-report` は不正な成功報告を拒否した非終端状態。後続の妥当な成功報告でreasonをクリアし、
  収束なら`converged`、未収束なら経過時間に応じて`pending` / `delayed`へ遷移する。
- `reason`は`failed-retryable`または`invalid-report`の現在診断に対応する場合だけ保持し、
  妥当な成功報告でそれらの診断が解消された後に過去理由を残さない。
- desired は診断状態によって巻き戻さない。
- applied が最新 desired と一致した時だけ `converged`。

## 8. DesiredSubscription

Router 起点の Connect server-streaming RPC `PortalRouterService.StreamDesired`。

| Field | Type | Rules |
|---|---|---|
| `subscriptionId` | opaque string | 接続単位 |
| `team` | TeamKey | 購読対象 |
| `lastSentSnapshotId` | string | 最後に送信した desired |
| `connectedAt` | timestamp | 診断用 |

**Lifecycle**

1. 接続時に対象チームの最新 ConfigSnapshot を必ず送信。
2. desired 更新時に最新値を送信。送信待ち中の中間スナップショットは coalesce 可。
3. reset 時は接続を維持し、新しい初期 ConfigSnapshot を配信する。
4. 切断・再接続後も接続時最新値から収束可能。

## 9. BenchmarkState

### BenchmarkStatus

`idle | pending | running`

### BenchmarkRuntime

| Field | Type | Rules |
|---|---|---|
| `status` | BenchmarkStatus | 状態機械に従う |
| `progress` | integer 0..100 | idle/pending 初期 0 |
| `activeRunId` | string / null | 実行中だけ設定 |
| `startedAt` | timestamp / null | 実行開始時 |
| `lockReason` | string / null | running/pending 中は設定固定理由 |
| `sequenceIndex` | non-negative integer | 決定的結果列の次位置 |

**State transitions**

```text
idle
  └─ start (converged && unlocked) → pending
pending
  └─ worker starts → running
running
  └─ complete → idle + BenchmarkRun prepend + score summary update

any non-reset state
  └─ reset → idle(progress=0, history empty, sequenceIndex=0)
```

**Rejected transitions**

- applied 不明または desired/applied 未収束中の start。
- pending/running 中の追加 start。
- pending/running 中の設定変更。

## 10. BenchmarkRun

| Field | Type | Rules |
|---|---|---|
| `id` | opaque string | 実行ごとに一意 |
| `sequence` | positive integer | reset 後 1 から増加 |
| `startedAt` | timestamp | 表示用 |
| `completedAt` | timestamp | startedAt 以上 |
| `mainServer` | ServerID | 実行開始時の applied |
| `mainServerRegion` | Region | 実行開始時の applied |
| `targetHostname` | string | 実行開始時の applied |
| `score` | non-negative integer | FAIL 時 0 を許可 |
| `result` | `PASS | FAIL` | 固定シード結果列 |
| `logs` | array(string) | 展開表示可能、決定的 |

### ScoreSummary

| Field | Type | Rules |
|---|---|---|
| `bestScore` | integer / null | PASS/FAIL を問わず最大。履歴空は null |
| `latestScore` | integer / null | 直近実行。履歴空は null |
| `runsCount` | non-negative integer | history の件数と一致 |

## 11. PortalState

モックポータルのUIが`PortalMockService.GetState` / `StreamState`で参照する状態スナップショット。
Router向けRPCでは使用しない。

| Field | Type |
|---|---|
| `team` | TeamKey |
| `desired` | ConfigSnapshot |
| `applied` | AppliedConfig |
| `apply` | ApplyDiagnostic |
| `converged` | boolean |
| `locked` | boolean |
| `lockReason` | string / null |
| `supportedCountries` | array(SupportedCountry) |

**Cross-entity invariants**

1. `converged=true` iff applied.config が現在 desired.config と一致。
2. `locked=true` iff benchmark.status が pending/running。
3. UI の applied 表示は PortalState.applied だけから構成し、desired を代用しない。
4. reset 前の snapshotId を含む成功報告も、Configが妥当ならRouter の実状態として applied へ反映する。

## 12. MockPortalState / MockRuntime

モック固有の起動情報・ベンチシミュレーション状態。
`portal_mock/v1.PortalMockService.GetState` / `StreamState` / `Reset` で公開する。

| Field | Type | Rules |
|---|---|---|
| `routerMode` | `stand-in | external-router` | 起動時固定 |
| `standInProfile` | profile / null | stand-in のみ。起動時固定 |
| `processStartedAt` | timestamp | プロセス開始時刻。reset で変化しない |

### MockPortalState

| Field | Type |
|---|---|
| `runtime` | MockRuntime |
| `portal` | portal_mock.v1.PortalState |
| `benchmark` | BenchmarkRuntime |
| `scores` | ScoreSummary |
| `history` | array(BenchmarkRun), newest first |

**Invariant**

- `external-router` では `standInProfile` は未設定。
- mode/profile は reset で変更せず、変更には再起動が必要。
- Router はこの message に依存せず、共有 `portal/v1` だけで連携する。
- 実ベンチマーカーとの通信は行わない。BenchmarkRuntime/Run は UI シミュレーションだけの状態。
- history は `completedAt` の新しい順。
- `scores.runsCount == len(history)`。

## 13. 初期化

### Common desired

```text
snapshotId: reset ごとに新規発行
server1: AP
server2: AP
server3: AP
mainServer: server1
defaultCountry: JP (region AP)
benchmark: idle / progress 0
history: []
scores: best=null, latest=null, count=0
```

### stand-in mode

- desired.config と applied.config は同一。
- apply state は `converged`。

### external-router mode

- applied.snapshot は未設定。
- apply state は `unsynced`。Router の成功報告まで未収束。
