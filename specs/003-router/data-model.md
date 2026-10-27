# Data Model: 擬似地理分散 Router

**Feature**: `003-router`

**Date**: 2026-08-04

永続 DB は持たない。ここでの entity は Portal snapshot、Router の reconcile state、network/xDS candidate を表す。

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

## RDSCandidate

Fields:

- `version`: snapshot ID と generation から作る一意な non-secret string。
- `routeConfigurationName`: bootstrap と一致する固定値。
- 4 virtual hosts（app、server1、server2、server3）と invalid-host catch-all。
- 各 host に10 country route、header-absent default route、invalid-country route。
- route ごとの target cluster、header mutation、fault、bandwidth profile。

State: `built → published → ACKed`、または `published → NACKed/timed-out → rollback`。

## ApplyAttempt

Fields:

- snapshot ID / generation
- phase: `validate`, `network`, `xds-publish`, `xds-ack`, `commit`, `report`
- started/completed timestamps
- outcome: `success`, `retryable-failure`, `superseded`
- non-secret reason
- retry count

Transitions:

```text
received
  -> validated
  -> network-applied
  -> rds-published
  -> rds-acked
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
- ACKed RDS version
- committed timestamp

Invariants:

- nil until first complete apply.
- readiness is true iff ActiveSnapshot exists and the current Envoy stream has ACKed an RDS snapshot; disconnect/reconnect では再 ACK まで false。
- failure of a later attempt does not mutate ActiveSnapshot。

## ControllerState

Fields:

- health: process/reconcile loop alive
- ready: derived from ActiveSnapshot
- portal connected flag
- latest desired snapshot metadata
- active snapshot metadata
- last attempt phase/outcome/reason/retry
- Envoy stream connected flag and last ACK/NACK

Concurrency:

- one apply worker owns kernel and xDS mutation.
- stream reader only replaces the pending latest snapshot.
- status reads immutable copies under synchronization.
- generation is checked before active commit; stale generation cannot replace a newer committed generation.
