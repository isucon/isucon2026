# Router

構成は app1=AP、app2=EU、app3=US、db1=AP の4台で固定です。db1 は共有 PostgreSQL 専用で HTTP routing の対象外です。

Portal の desired config に追従し、HAProxy の HTTP routing/shaping と Linux の server 間 routing/shaping を一体で管理する
擬似地理分散 Router である。実装の構成と rollback 境界は [設計文書](../docs/router.md) を参照する。

## 本番の設定配信サービス

`cmd/router-control` は、競技環境の Router サーバーで動く設定配信サービスです。
バイナリと systemd unit の名前は `isureal-router-control` です。
競技用の Web ポータル（`portal/`）とは別のプロセスです。

```text
isureal-router-control (127.0.0.1:18082)
    └─ 設定配信 → isureal-router → HAProxy と Linux の通信制御
    └─ 適用結果 ← isureal-router
```

配信する地域配置は app1=AP、app2=EU、app3=US、db1=AP に固定しています。
既定国は JP です。
設定は起動中に変更せず、環境変数を変更してサービスを再起動すると新しい snapshot を配信します。
Router は再接続して設定を適用します。
適用結果は構成と snapshot ID を検証し、journal に記録します。
状態を DB に永続化する処理はありません。

| 環境変数 | 内容 |
|---|---|
| `ROUTER_CONTROL_TEAM_NUMBER` | 必須。正のチーム番号 |
| `ROUTER_CONTROL_LISTEN_ADDRESS` | 既定 `127.0.0.1:18082`。loopback IP のみ指定可能 |
| `ROUTER_CONTROL_DEFAULT_COUNTRY` | 既定 `JP`。Router が対応する国コード |

設定ファイルは `/etc/isucon/router-control.env` です。
認証を提供しないローカル専用サービスのため、外部向けのポート転送は行わないでください。
`GET /healthz` は設定配信プロセスの生存確認です。
Router 自身の設定適用は `isureal-router` の `/readyz` で確認してください。

既存 Router との互換性のため、通信上のサービス名 `portal.v1.PortalRouterService` と
Router 側の環境変数名 `ROUTER_PORTAL_ENDPOINT` は維持しています。
旧モックの UI、模擬ベンチマーク、Router 代替動作、`portal_mock.v1` API は提供しません。

### 配備と旧構成からの移行

`provisioning/ansible/prepare.yml` が両方の Go バイナリをビルドし、Router AMI に配置します。
Pulumi の user-data と参加者用 CloudFormation は、新しい unit と環境変数を使用します。

1. 新しいコードで Router AMI をビルドします。
2. 作問用 Pulumi の `routerAmiId`、またはポータルの `ISUXPORTAL_ROUTER_AMI_ID` を新しい AMI に設定します。
3. Pulumi の変更を適用します。ポータルの環境変数を変えた場合は ecspresso でポータルも再デプロイします。
4. 新規の検証用競技環境を作成し、両サービスの起動、Router の `/readyz`、実ベンチの成功を確認します。

古い AMI には `isureal-router-control` がないため、新しいテンプレートと組み合わせないでください。
既存の競技インスタンスをこのソース変更だけで自動更新する処理はありません。
旧 `isureal-portal` との同時起動はポートが競合するため避けてください。

## 検証

Go と Docker が必要です。

```bash
make router-check
```

`router-check` は go vet/test/build と、HTTP/HTTPS 用の HAProxy 設定を検証します。
旧モックに依存した Compose E2E は削除しています。

## Source layout

```text
cmd/router/             process wiring
cmd/router-control/     production configuration service
internal/control/       fixed configuration stream and report validation
internal/config/        strict environment/topology configuration
internal/domain/        fixed region/server/country/snapshot invariants
internal/netcontrol/    interface discovery, nftables, HTB/netem, rollback
internal/haproxy/       HAProxy config rendering, runtime API (default country)
internal/reconcile/     Portal stream, latest-wins apply, report retry
internal/status/        /healthz and /readyz
digest/                 isureal-digest (Rust): digests of the benchmark's image downloads
```

## Runtime configuration

未設定時は team 1 と `abc123` の開発用 hostname、および production 相当の traffic profile を使う。すべて起動時に
検証され、不正値が1つでもあれば side effect 前に終了する。

### Control plane / process

| Environment variable | Default | Meaning |
|---|---|---|
| `ROUTER_PORTAL_ENDPOINT` | `http://127.0.0.1:18082` | Router control Connect base URL |
| `ROUTER_TEAM_NUMBER` | `1` | 正の TeamKey number |
| `ROUTER_STATUS_LISTEN_ADDRESS` | `0.0.0.0:18081` | health/readiness listener |
| `ROUTER_COMMAND_TIMEOUT` | `5s` | 個々の nft/tc/sysctl command と HAProxy runtime API の timeout |
| `ROUTER_REPORT_RETRY_INTERVAL` | `500ms` | apply/report retry interval。HAProxy の既定の国を確かめる間隔も兼ねる |
| `ROUTER_PORTAL_RECONNECT_MIN_BACKOFF` | `100ms` | stream reconnect 初期 backoff |
| `ROUTER_PORTAL_RECONNECT_MAX_BACKOFF` | `5s` | stream reconnect 最大 backoff |

duration は Go duration 形式で指定する。

### HAProxy

HAProxy の service は起動のたびに `isureal-router haproxy-config` を実行し、Router と同じ環境変数から
`ROUTER_HAPROXY_DIR` に `haproxy.cfg`、遅延用の Lua、空の既定の国の map を書く。Controller は既定の国だけを
runtime API で書き換える。

| Environment variable | Default | Meaning |
|---|---|---|
| `ROUTER_HTTP_LISTEN_ADDRESS` | `0.0.0.0:8080` | HTTP の待ち受け |
| `ROUTER_HTTPS_LISTEN_ADDRESS` | 空（無効） | HTTPS の待ち受け |
| `ROUTER_TLS_CERTIFICATE` | 空 | 証明書のチェーンと鍵を続けて入れた PEM の絶対パス。HTTPS を使うときは必須 |
| `ROUTER_UPSTREAM_PORT` | `8080` | app1〜3 の HTTP port |
| `ROUTER_HAPROXY_DIR` | `/run/isureal-haproxy` | HAProxy が読むファイルの置き場所 |
| `ROUTER_HAPROXY_RUNTIME_ADDRESS` | `127.0.0.1:18001` | HAProxy runtime API（TCP） |
| `ROUTER_DIGEST_ADDRESS` | `127.0.0.1:18010` | isureal-digest の待ち受け。空にするとベンチの画像のダウンロードもアプリへ送る |

ベンチの画像とエクスポートのダウンロード（`X-Benchmark-Request-Id` 付き）は、同じ network namespace で動く
`isureal-digest`（`digest/`、Rust）に送り、本体の代わりに XXH3 を返す（初期データの画像で
`X-Benchmark-Digest: sha256` が付いていれば SHA-256。[設計文書](../docs/router.md) の「ベンチの画像のダウンロード」）。isureal-digest は上の 2 つのアドレスだけを環境変数から読む。単体テストは
`cargo test --manifest-path router/digest/Cargo.toml`。

### Host

Host 名は設定しない。HAProxy は Host が `asia.`、`europe.`、`america.` で始まる要求をそれぞれ app1、app2、app3 へ送り、
それ以外の要求（IP アドレスや Host のない要求を含む）は `X-Country-Code` の国、なければ Portal の既定の国で振り分ける。
接頭辞は大文字小文字を区別しない。`asia.<Router の IP>.nip.io` のような名前を使えば、DNS を用意しなくても個別のサーバーへ届く。

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

| Environment variable | Production default |
|---|---:|
| `ROUTER_SAME_REGION_DELAY` | `0s` |
| `ROUTER_SAME_REGION_BANDWIDTH_BPS` | `1000000000` |
| `ROUTER_AP_EU_DELAY` | `100ms` |
| `ROUTER_AP_EU_BANDWIDTH_BPS` | `200000000` |
| `ROUTER_EU_US_DELAY` | `100ms` |
| `ROUTER_EU_US_BANDWIDTH_BPS` | `200000000` |
| `ROUTER_AP_US_DELAY` | `100ms` |
| `ROUTER_AP_US_BANDWIDTH_BPS` | `200000000` |

delay は end-to-end の追加遅延を表す。HTTP は HAProxy が設定値を1回注入し（ミリ秒単位に切り上げる）、サーバー間通信は往復で設定値に
なるよう tc が各方向へ半分ずつ注入する（production の `100ms` は片道50 ms、RTT 100 ms）。
bandwidth は bit/s で、正数かつ各ペアが same 以下でなければならない。delay は0または1 ms以上を受け付ける。

## Health and diagnostics

- `GET /healthz`: process が稼働中なら `200`。
- `GET /readyz`: Portal snapshot が network へ反映され、要求の時点で HAProxy がその既定の国を使っていれば `200`。
- 後続 apply が失敗しても last-known-good が利用可能なら ready は維持し、body の `phase` / `last_error` に失敗を残す。

## Deployment requirements and limitations

- Router namespace へ namespaced `net.ipv4.ip_forward=1` を起動時に設定し、Router に `NET_ADMIN` を付与する。
- HAProxy は同じ namespace で、Router と同じ環境変数を使って起動する（`router/Dockerfile` の `haproxy` target、
  AWS では `isureal-haproxy.service`）。
- 各 server は専用 segment だけに接続し、peer subnet の next hop を対応 Router local IP にする。
- Controller state は memory のみである。再起動後は Portal から snapshot を受けて再構築するまで ready にならない。
- HTTPS は `ROUTER_HTTPS_LISTEN_ADDRESS` と証明書を渡したときだけ有効になる。WAF/外部認証/HA/長期 metrics は
  deployment 側の責務である。
- HAProxy の bandwidth limit は route（宛先と国の組）単位で、要求と応答のそれぞれにかかる。本 feature は1要求・1経路の分類と上限を保証し、複数要求を束ねた
  region-wide aggregate rate の厳密性は後続の競技 deployment で決定する。
