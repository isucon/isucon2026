# Research: 擬似地理分散 Router

**Feature**: `003-router`

**Date**: 2026-08-04

## Decision 1: 参照実装から採用する境界

**Decision**: `~/ad-private` から、Portal/Core を pull する reconcile loop、実 IP から interface を解決する方式、
冪等な network 設定、nftables の一括反映、go-control-plane snapshot cache と xDS server という責務分離を採用する。

**Rationale**: Router 固有の kernel 権限を小さな service に閉じ、desired と実状態を繰り返し収束させる形が今回の
Portal 契約に一致する。参照側の `internal/router/network_linux.go`、firewall、repo reverse proxy が実績ある例になる。

**Alternatives considered**:

- 参照実装を丸ごと移植: WireGuard、参加者 VPN、repo、PCAP と今回無関係な責務を持ち込むため不採用。
- Portal が Router へ push: 既存 `StreamDesired` の Router 起点 pull と再接続モデルを壊すため不採用。

## Decision 2: Compose network topology

**Decision**: `server1_net`、`server2_net`、`server3_net` を internal bridge とし、それぞれ Router と該当 server
だけを参加させる。client edge と Portal control は別 network とする。Envoy は `network_mode: service:router` で
Router の namespace を共有する。Compose は interface 接続順で device 名が決まることを保証しないため、Controller は
固定 local IP を持つ interface を検索する。

**Rationale**: server 同士に共有 L2 を与えず、Envoy を別 network に参加させて backend を迂回させることもない。
Docker Compose は service network namespace の共有、明示 network と IPAM、internal network を公式に提供する。

**Alternatives considered**:

- 1つの shared bridge + firewall: L2 直接通信を完全には表現できず、Router-only invariant の証明が弱い。
- Envoy を各 server network へ直接接続: Router namespace を通らず、責務と経路観測が分離する。
- interface 名を `eth1` 等へ固定: Compose の `priority` は device 名順を保証しないため不採用。

References:

- https://docs.docker.com/reference/compose-file/services/#network_mode
- https://docs.docker.com/reference/compose-file/networks/

## Decision 3: Linux server-to-server shaping

**Decision**: IPv4 forwarding と専用 nftables forward chain を Router namespace で管理する。各 server 向け egress に
HTB root/class と netem leaf を作り、source server IP を region class へ分類する。同一 region は追加 delay なし／
1 Gbps、異 region は200 ms／200 Mbps。E2E では値だけ縮尺変更する。

**Rationale**: destination interface で shaping すれば両方向の各 packet を同じ規則で扱え、server の配置変更は class
mapping の再生成だけで済む。nft batch は ruleset を一括適用できる。

**Alternatives considered**:

- application 側で sleep/throttle: DB 等の任意 TCP 通信を再現できない。
- TBF だけを root に設定: source region ごとの分類と共有上限を同時に表現しにくい。
- host namespace を操作: E2E project 外の network を破損し得るため不採用。

Reference: https://wiki.nftables.org/wiki-nftables/index.php/Operations_at_ruleset_level

## Decision 4: HTTP routing and shaping

**Decision**: Envoy listener と server1〜3 cluster は bootstrap の static resource とする。Host×country の RDS
RouteConfiguration だけを Controller が動的配信する。route ごとに HTTP fault の fixed delay と bandwidth limit を
設定する。対応国 exact-match、header absent default route、invalid catch-all direct response の順で評価する。

**Rationale**: main server、default country、region placementだけが変動するため、RDS 一種類なら依存順序、rollback、
ACK/NACK 相関が明瞭になる。公式 API は route-level `typed_per_filter_config` と route 固有 bandwidth limit を提供する。

**Alternatives considered**:

- static file の書換え + Envoy restart: 接続を中断し、snapshot 単位の ACK/NACK を得られない。
- 全 listener/cluster/route を ADS: 固定 resource まで更新対象にし、atomicity とテスト範囲が不要に増える。
- client が fault 制御 header を指定: 参加者が制約を迂回・増幅できるため不採用。

References:

- https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/route/v3/route.proto.html
- https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/bandwidth_limit/v3/bandwidth_limit.proto.html
- https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol.html

## Decision 5: xDS acknowledgement boundary

**Decision**: snapshot ID から一意な RDS version を作り、go-control-plane callback で Envoy の対象 response に対する
ACK/NACK を追跡する。ACK timeout/NACK は apply failure とする。ACK は resource の妥当性受理であり転送成功保証では
ないため、Controller readiness と E2E probe を別に検証する。

**Rationale**: snapshot cache への publish だけで成功報告すると Envoy が NACK した状態を Portal の applied と誤認する。
xDS protocol は nonce と error detail による ACK/NACK を定義している。

**Alternatives considered**:

- publish 即成功: desired/applied の意味を壊す。
- admin config dump の polling だけ: version と NACK reason の厳密な相関が弱い。

## Decision 6: Latest-wins reconciliation and rollback

**Decision**: stream reader と単一 apply worker を分離し、容量1 channel で未処理 desired を最新へ coalesce する。
apply 中に新しい snapshot が届いた場合は次処理として保持し、active 更新時に generation を再確認する。network または
RDS が失敗した場合は last-known-good を再適用する。Portal report の再送は apply と分離する。

**Rationale**: Portal 契約は命令列ではなく最新状態への収束を保証する。直列 apply で kernel/xDS の競合を避けながら、
古い完了による巻戻しを防げる。

**Alternatives considered**:

- snapshot ごとの goroutine: kernel resource と RDS version の競合を招く。
- 中間 snapshot を全件適用: coalesce 可能という公開契約に対して不要な停止時間を増やす。

## Decision 7: E2E measurement

**Decision**: production default 200 ms／200 Mbps と1 Gbpsは維持し、E2E では cross 120 ms／1 Mbps、same 100 Mbps
等へ環境変数で縮尺変更する。HTTP は同じ fixture payload の elapsed/throughput、server 間は server 内部 fetch の
elapsed/throughput、経路は `ip route get`、隔離は Router namespace 停止時の新規接続失敗で判定する。

**Rationale**: 200 Mbps と1 Gbpsの差は共有 CI host で安定測定しにくい。小さい値でも route classification と適用機構は
同一で、短時間に決定的な差を作れる。

**Alternatives considered**:

- production 値だけを時間測定: host 性能が上限になり flake する。
- 設定 text の snapshot test だけ: 実 kernel/Envoy の動作を証明しない。

## Resolved Unknowns

- Envoy bandwidth filter の `limit_kbps` は API 文書上 KiB/s であり、domain の bit/s から切上げ変換する。
- per-route bandwidth limit は route ごとに独立するため、本番の「region ごとに共有」の厳密な aggregate 制御は
  後続競技仕様で評価する。本 feature は1要求／1経路の上限と分類の正しさを保証する。
- xDS ACK は apply 完了そのものではない。Portal 成功報告の技術的 gate とし、実転送は readiness/E2E で保証する。
- Docker network attachment priority は interface 名を固定しない。local IP discovery を必須にする。

未解決事項はない。
