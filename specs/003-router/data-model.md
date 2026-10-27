# Data Model: 擬似地理分散 Router

**Feature**: `003-router`

**Date**: 2026-08-04

永続 DB は持たない。ここでの entity は Portal snapshot、Router の reconcile state、network candidate と HAProxy の既定の国を表す。
（2026-10-27 に Envoy と RDS を HAProxy に置き換えた。research.md の Decision 8）

## Region

- Values: `US`, `AP`, `EU`
- `UNSPECIFIED` と未知値は境界で拒否する。

## ServerID

- Values: `server1`, `server2`, `server3`
- 各 ID は1つの backend address、専用 subnet、Router local address、server address を持つ。
- 3 server の ID/address/subnet は互いに重複してはならない。

## Country

| Code | Region |
|---|---|
| US, CA, MX | US |
| JP, KR, CN, AU | AP |
| GB, DE, FR | EU |

- code は2文字の大文字 ASCII かつ表に存在する値。
- HTTP header がない場合だけ `RouterConfig.defaultCountry` を用いる。
- 複数値、空、小文字、未知値は invalid。

## RouterConfig

Fields:

- `serverRegions: map<ServerID, Region>`: 3 ID がちょうど1回ずつ必須。
- `mainServer: ServerID`: 有効な1 ID。
- `defaultCountry: Country`: 対応表内の1 code。

Derived values:

- `region(serverID)`
- `region(country)`
- `target(host)`: app host は `mainServer`、server host は該当 ID。
- `class(sourceServer, destinationServer)`: region 同一なら same、異なれば cross。
- `class(country, targetServer)`: region 同一なら same、異なれば cross。

Validation は side effect 前に全項目へ行い、部分的に既定値を補わない。

## DesiredSnapshot

Fields:

- `snapshotID`: 空でない opaque string。
- `config`: validated `RouterConfig`。
- `issuedAt`: Portal correlation timestamp。順序判定には使わない。
- `generation`: Controller 内で受信順に増加する monotonic number（Portal へは送らない）。

Invariants:

- 同じ `snapshotID + config` の再受信は冪等。
- snapshot ID の辞書順・時刻で newer を推測しない。Controller generation が latest-wins を担う。

## NetworkTopology

Per server:

- `subnet`
- `routerLocalIP`
- `serverIP`
- runtime-resolved `interfaceName`

Invariants:

- subnet はIPv4 CIDRで重複しない。
- router/server IP は対応 subnet 内で異なる。
- interface は Router namespace 内で router local IP をちょうど1つ持つ。
- forward allow は異なる server interface の組だけ。control/edge は server forwarding allow に入れない。

## TrafficProfile

Fields:

- `sameRegion`（default delay 0 ms、bandwidth 1,000,000,000 bit/s）
- `apEu` / `euUs` / `apUs`（各 region pair。default delay 100 ms end-to-end、サーバー間tcでは片道50 msずつ。
  default bandwidth 200,000,000 bit/s。双方向で共通）

Validation:

- delay は0以上かつ configured maximum 以下。
- bandwidth は正数、各 region pair は same 以下。
- E2E override は値だけを変更し、classification を変更しない。

## NetworkCandidate

Fields:

- validated topology/interface mapping
- complete nft ruleset
- per-interface HTB/netem/filter command batch
- source `RouterConfig`

State: `rendered → validated → applied` または `failed → rollback`。

同じ candidate の再適用は最終 kernel state を変えない。

## HAProxy configuration

HAProxy の設定は Router の環境変数だけから作り、snapshot では変えない。

- 4 hosts（app、app1、app2、app3）と、それ以外の Host への 404。
- 10 country と header-absent の route。route の key は Host と国の組（header がなければ Host と `default`）。
- Host と client region の組ごとの target server、delay（Lua）、bandwidth filter（`bwlim-in`/`bwlim-out`）。

## DefaultCountry

Fields:

- HAProxy の runtime API で書き換える map（`$ROUTER_HAPROXY_DIR/default-country.map`）の `default` の値。

State: `empty（HAProxy の起動直後。要求は 503）→ set → verified（読み戻して一致）`。HAProxy が再起動すると empty に
戻り、apply worker が active snapshot の値を書き戻す。

## ApplyAttempt

Fields:

- snapshot ID / generation
- phase: `validate`, `network`, `proxy`, `commit`, `report`
- started/completed timestamps
- outcome: `success`, `retryable-failure`, `superseded`
- non-secret reason
- retry count

Transitions:

```text
received
  -> validated
  -> network-applied
  -> default-country-verified
  -> active-committed
  -> reported

any pre-commit failure -> rollback last-known-good -> failure-reported -> retry
newer generation before commit -> rollback/finish safely -> superseded -> process latest
report transport failure after commit -> keep active -> retry report only
```

## ActiveSnapshot

Fields:

- `DesiredSnapshot` correlation
- actual `RouterConfig`
- committed network candidate identity
- committed timestamp

Invariants:

- nil until first complete apply.
- readiness is true iff ActiveSnapshot exists and HAProxy uses its default country when `/readyz` is requested。
- failure of a later attempt does not mutate ActiveSnapshot。

## ControllerState

Fields:

- health: process/reconcile loop alive
- ready: derived from ActiveSnapshot
- portal connected flag
- latest desired snapshot metadata
- active snapshot metadata
- last attempt phase/outcome/reason/retry
- HAProxy check result（`/readyz` の要求ごと）

Concurrency:

- one apply worker owns kernel and HAProxy mutation, including restoring the default country after a HAProxy restart.
- stream reader only replaces the pending latest snapshot.
- status reads immutable copies under synchronization.
- generation is checked before active commit; stale generation cannot replace a newer committed generation.
