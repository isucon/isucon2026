# Router

構成は app1=AP、app2=EU、app3=US、db1=AP の4台で固定です。db1 は共有 PostgreSQL 専用で HTTP routing の対象外です。

Portal の desired config に追従し、Envoy の HTTP routing/shaping と Linux の server 間 routing/shaping を一体で管理する
擬似地理分散 Router である。実装の構成と rollback 境界は [設計文書](../docs/router.md) を参照する。

## すぐに検証する

前提は Linux、Docker Engine + Docker Compose v2、`NET_ADMIN` container と HTB/netem/nftables を利用できる kernel である。
host で静的 gate を実行する場合は Go 1.26.5 も必要になる。

```bash
make router-check
make router-e2e
```

`router-check` は go vet/test/build、Compose model、Envoy bootstrap を検証する。`router-e2e` は Portal Mock、Router、
Envoy、3つの server segment、runner を project `isureal-router-e2e` に構築し、routing、実測 shaping、動的変更、
再起動回復、Router 停止中の隔離を自動判定して cleanup する。既存の root/Portal Mock Compose project は停止しない。

失敗後に container と network を残して調査する場合だけ次を使う。

```bash
ROUTER_E2E_KEEP_ON_FAILURE=1 router/tests/e2e/run.sh
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml ps --all
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml logs --no-color router envoy portal
```

失敗ログは `router/.e2e-artifacts/compose.log` にも保存される。調査後の cleanup は次で行う。

```bash
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  down --volumes --remove-orphans
```

より詳細な検証順序は [quickstart](../specs/003-router/quickstart.md) にある。

## Source layout

```text
cmd/router/             process wiring
internal/config/        strict environment/topology configuration
internal/domain/        fixed region/server/country/snapshot invariants
internal/netcontrol/    interface discovery, nftables, HTB/netem, rollback
internal/xds/           RDS builder, xDS server, ACK/NACK correlation
internal/reconcile/     Portal stream, latest-wins apply, report retry
internal/status/        /healthz and /readyz
envoy/bootstrap.yaml    static listener/xDS/backend clusters
test-fixtures/server/   bounded E2E-only backend and peer probe
tests/e2e/              compiled assertions and Compose orchestration
compose.e2e.yaml        isolated five-network topology
```

production binary には fixture endpoint や Docker API client を含めない。

## Runtime configuration

未設定時は team 1 と `abc123` の開発用 hostname、および production 相当の traffic profile を使う。すべて起動時に
検証され、不正値が1つでもあれば side effect 前に終了する。

### Control plane / process

| Environment variable | Default | Meaning |
|---|---|---|
| `ROUTER_PORTAL_ENDPOINT` | `http://portal:8080` | Portal Connect base URL |
| `ROUTER_TEAM_NUMBER` | `1` | 正の TeamKey number |
| `ROUTER_XDS_LISTEN_ADDRESS` | `0.0.0.0:18000` | Envoy ADS/RDS gRPC listener |
| `ROUTER_STATUS_LISTEN_ADDRESS` | `0.0.0.0:18081` | health/readiness listener |
| `ROUTER_ENVOY_NODE_ID` | `isureal-router-envoy` | bootstrap と一致する xDS node ID |
| `ROUTER_ROUTE_CONFIG_NAME` | `isureal_routes` | bootstrap と一致する RDS resource name |
| `ROUTER_XDS_ACK_TIMEOUT` | `5s` | RDS version ACK の上限待ち時間 |
| `ROUTER_COMMAND_TIMEOUT` | `5s` | 個々の nft/tc/sysctl command timeout |
| `ROUTER_REPORT_RETRY_INTERVAL` | `500ms` | apply/report retry interval |
| `ROUTER_PORTAL_RECONNECT_MIN_BACKOFF` | `100ms` | stream reconnect 初期 backoff |
| `ROUTER_PORTAL_RECONNECT_MAX_BACKOFF` | `5s` | stream reconnect 最大 backoff |

duration は Go duration 形式で指定する。

### Public hostnames

| Environment variable | Default |
|---|---|
| `ROUTER_APP_HOST` | `app.team1-abc123.isucon2026.westens.tokyo` |
| `ROUTER_APP1_HOST` | `app1.team1-abc123.isucon2026.westens.tokyo` |
| `ROUTER_APP2_HOST` | `app2.team1-abc123.isucon2026.westens.tokyo` |
| `ROUTER_APP3_HOST` | `app3.team1-abc123.isucon2026.westens.tokyo` |

4値は妥当な小文字 hostname かつ相互に異なる必要がある。Envoy はvirtual-host照合時にHostのportを無視するため、同じhostnameを任意の公開port経由で利用できる。

### Server topology

各 `N` は `1`、`2`、`3` のいずれかである。

| Environment variable | app1 default | app2 default | app3 default |
|---|---|---|---|
| `ROUTER_SERVERN_SUBNET` | `172.31.1.0/24` | `172.31.2.0/24` | `172.31.3.0/24` |
| `ROUTER_SERVERN_REGION_LOCAL_IP` | `172.31.1.2` | `172.31.2.2` | `172.31.3.2` |
| `ROUTER_SERVERN_IP` | `172.31.1.10` | `172.31.2.10` | `172.31.3.10` |

subnet は canonical IPv4 prefix、両 IP は対応 subnet 内の異なる値、3 subnet は非重複でなければならない。
Controller は region local IP から interface 名を解決するため、Compose の `ethN` 順には依存しない。

### Traffic profile

遅延と帯域は同一リージョンと、AP（asia）・EU（europe）・US（america）の3つのリージョンペアごとに設定する。
ペアの設定は双方向に共通で、AP-EU は AP→EU と EU→AP の両方に適用される。

| Environment variable | Production default | E2E override |
|---|---:|---:|
| `ROUTER_SAME_REGION_DELAY` | `0s` | `0s` |
| `ROUTER_SAME_REGION_BANDWIDTH_BPS` | `1000000000` | `100000000` |
| `ROUTER_AP_EU_DELAY` | `100ms` | `120ms` |
| `ROUTER_AP_EU_BANDWIDTH_BPS` | `200000000` | `1000000` |
| `ROUTER_EU_US_DELAY` | `100ms` | `120ms` |
| `ROUTER_EU_US_BANDWIDTH_BPS` | `200000000` | `1000000` |
| `ROUTER_AP_US_DELAY` | `100ms` | `120ms` |
| `ROUTER_AP_US_BANDWIDTH_BPS` | `200000000` | `1000000` |

delay は end-to-end の追加遅延を表す。HTTP は Envoy が設定値を1回注入し、サーバー間通信は往復で設定値に
なるよう tc が各方向へ半分ずつ注入する（production の `100ms` は片道50 ms、RTT 100 ms）。
bandwidth は bit/s で、正数かつ各ペアが same 以下でなければならない。delay は0または1 ms以上を受け付ける。
E2E override は class 判定を変えず、共有 CI host でも差を測定できるよう値だけを縮尺する。

## Health and diagnostics

- `GET /healthz`: process が稼働中なら `200`。
- `GET /readyz`: Portal snapshot が network と ACK 済み RDS の双方へ反映され、Envoy が接続中なら `200`。
- 後続 apply が失敗しても last-known-good が利用可能なら ready は維持し、body の `phase` / `last_error` に失敗を残す。

E2E を保持した状態で確認する例:

```bash
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router wget -q -O - http://127.0.0.1:18081/readyz
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router nft list table inet isureal_router
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router tc -s qdisc show
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router wget -q -O - http://127.0.0.1:9901/config_dump
```

Router と Envoy は namespace を共有するため、Envoy admin は Router container の loopback から確認できる。

## Deployment requirements and limitations

- Router namespace へ namespaced `net.ipv4.ip_forward=1` を起動時に設定し、Router に `NET_ADMIN` を付与する。
- Envoy は同じ namespace で起動し、`envoy/bootstrap.yaml` と node/resource 名を一致させる。
- 各 server は専用 segment だけに接続し、peer subnet の next hop を対応 Router local IP にする。
- Controller state は memory のみである。再起動後は Portal から snapshot を受けて再構築するまで ready にならない。
- HTTP は平文である。TLS/WAF/外部認証/HA/長期 metrics は deployment 側の責務である。
- Envoy bandwidth limit は route 単位である。本 feature は1要求・1経路の分類と上限を保証し、複数要求を束ねた
  region-wide aggregate rate の厳密性は後続の競技 deployment で決定する。
