# 擬似地理分散 Router 設計

Router は、単一ホスト上の4台のサーバーを app1=AP、app2=EU、app3=US、db1=AP に配置したものとして扱い、
HTTP 通信とサーバー間通信の両方へ同じリージョン判定を適用する支援インフラである。Portal の desired config を
唯一の入力とし、Linux network data plane と Envoy data plane が同じ snapshot へ収束した後だけ applied として報告する。

アプリ3台は別々のリージョン、共有 PostgreSQL 専用の db1 は AP に固定され、ユーザーは配置を変更できない。Portal では配置を表示するだけとし、
リージョン更新要求は `invalid_argument` で拒否する。snapshot の `server_regions` は固定配置の確認用に維持し、
Portal と Router は固定値に一致しない設定を拒否する。変更可能なのは `defaultCountry` のみである。
メインサーバーは設けず、初回設定の適用時から3台すべてが国別ルーティングの対象になる。

実装は `router/` の独立 Go module にあり、公開制御契約は既存の `portal.v1.PortalRouterService` を再利用する。
本設計は開発・検証基盤を対象とし、TLS、VPN、複数チーム、本番 HA、採点は範囲外である。

## 構成

```text
                   control_net (internal)
        +--------+                         +--------------------+
        | Portal |<--- Connect stream ---->| Router Controller  |
        +--------+                         |                    |
                                           | xDS :18000         |
                   edge_net (internal)     | status :18081      |
        +--------+       HTTP :8080         | nftables / tc      |
        | client |------------------------->|                    |
        +--------+                          +---------+----------+
                                                     ^
                                        shared network namespace
                                                     v
                                               +-----------+
                                               |   Envoy   |
                                               +-----------+
                                                     |
                 +--------------------+--------------------+--------------------+
                 |                    |                    |
          app1_net          app2_net          app3_net
          172.31.1/24          172.31.2/24          172.31.3/24
                 |                    |                    |
             app1              app2              app3
            .1.10/24             .2.10/24             .3.10/24
          Router .1.2          Router .2.2          Router .3.2
```

図のアプリ用 segment に加え、`db1_net`（172.31.4.0/24、db1=.4.10、Router=.4.2）を接続する。
`app1_net`〜`app3_net` と `db1_net` は相互に L2 を共有しない。各 server は peer subnet への route の next hop を自身の
segment にある Router IP とする。Envoy は別 container だが `network_mode: service:router` で Router の network
namespace を共有するため、固定 backend cluster への通信も Router namespace から出る。

Compose の interface 名には依存しない。Controller は `172.31.1.2` などの設定済み local IPv4 を持つ interface を
実行時に検索し、重複または欠落を apply failure とする。

## 責務と権限境界

| Component | 主な責務 | 権限・到達範囲 |
|---|---|---|
| Portal | desired snapshot 配信、applied/failure 受理 | control network のみ |
| Router Controller | 検証、latest-wins reconcile、nft/tc、xDS、診断 | `NET_ADMIN`。Docker socket と host network は持たない |
| Envoy | Host/国別 routing、HTTP delay/rate | 非 root、追加 capability なし。Router namespace を共有 |
| app1〜3 | アプリを表す E2E fixture | 各専用 segment のみ。E2E では static route 設定用 `NET_ADMIN` のみ |
| db1 | DB ネットワーク検証用 fixture（E2E のみ HTTP を使用。本配備は PostgreSQL 16） | 専用 segment のみ。公開 HTTP cluster/Host は持たない |
| E2E runner | Connect/HTTP の外部観測と自動判定 | control/edge のみ。server segment と Docker socket へ接続しない |

全 Compose network は `internal: true` で、host port を公開しない。Router と fixture は `privileged` を使わず、
必要な capability を `NET_ADMIN` に限定する。Portal、Envoy、runner は capability をすべて drop する。

## HTTP data plane

Envoy bootstrap は listener、xDS cluster、app1〜3 cluster だけを固定する。Controller は次の RDS route matrix を
snapshot ごとに生成する。

- app Host はクライアントの国に対応するリージョンへ送る。JP/KR/CN/AU は app1 (AP)、GB/DE/FR は app2 (EU)、US/CA/MX は app3 (US) を使う。
- app1〜3 Host は各固定 server cluster へ送る。db1 は HTTP routing の対象にせず、db1 Host は 404 とする。
- `X-Country-Code` は US/CA/MX、JP/KR/CN/AU、GB/DE/FR の10値だけを exact match する。
- header がない場合だけ Portal の `defaultCountry` を1値挿入する。
- 空値、未知値、小文字、複数値は `400`、未知 Host は `404` を Envoy が直接返す。
- client country と target server の region が同じ route は same profile、異なる route はその region pair の profile を使う。
- delay は HTTP fault filter、rate は bandwidth-limit filter の route-level override で適用する。

Envoy が対象 version/nonce を ACK するまで RDS は commit 済みと扱わない。NACK の error detail と timeout は apply
failure として bounded な理由を Portal へ返す。ACK は設定受理の証明であり上流の生存証明ではないため、実転送は
readiness と E2E probe で別途確認する。暗黙の upstream failover は行わない。

## サーバー間 data plane

Router は namespaced `net.ipv4.ip_forward=1` を確認し、専用 `inet isureal_router` table の forward chain を管理する。
許可するのは異なる4つの server interface 間の12方向と established/related reply だけで、edge/control interface は
forward allow に含めない。

各 destination interface の egress には HTB root を置き、US・AP・EU の class と netem leaf を作る。source server IP
の u32 filter で source region class を選び、source/destination server region の組から delay/rate を決める。
app1↔db1 の2方向は same profile、それ以外の10方向は AP-EU・EU-US・AP-US のいずれかの region pair profile を使う。
pair profile は双方向で共通である。

既存 child を持つ HTB root は in-place replace できないため、更新 transaction ごとに Router 専用 root qdisc を
削除して完全な tree を再構築する。途中失敗時は直前の candidate について同じ処理を行い、last-known-good を戻す。
nft batch は一括検査後に table 単位で反映する。tc の複数 interface 更新自体は kernel 上で原子的ではないため、
外部 commit と成功報告は全 interface と RDS が成功した後に限定し、失敗時 rollback で整合させる。

production default は次のとおりである。各値は Router の環境変数で個別に変更できる。

| Classification | Additional end-to-end delay | Bandwidth ceiling |
|---|---:|---:|
| same region | 0 ms | 1 Gbps |
| AP↔EU | 100 ms | 200 Mbps |
| EU↔US | 100 ms | 200 Mbps |
| AP↔US | 100 ms | 200 Mbps |

サーバー間の100 msはRTTであり、Routerは各方向のtcへ50 msずつ設定する。クライアントからのHTTP通信では、
Envoyがリクエスト処理へ100 msを1回注入する。

E2E は分類 code path を変えず、測定しやすい 0/120 ms と 100/1 Mbps へ値だけを縮尺変更する。

## Reconciliation と rollback

Controller は Portal stream reader、単一 apply worker、report retry loop を分離する。pending queue は容量1で、未処理の
中間 snapshot は最新値へ coalesce される。kernel と xDS を変更する writer は常に1つである。

```text
StreamDesired
  -> protobuf/domain 全体検証
  -> interface 解決 + nft/tc/RDS candidate 生成
  -> network candidate 適用・確認
  -> RDS publish
  -> version/nonce 対応 ACK 待機
  -> ActiveSnapshot commit + ready
  -> ReportApplied(success)

network/RDS failure
  -> network と RDS を last-known-good へ rollback
  -> ReportApplied(failure)
  -> bounded interval で retry（より新しい pending があればそちらを優先）

report transport failure after commit
  -> data plane は維持
  -> report だけを再送
```

同じ snapshot ID と config の再受信は再適用せず成功報告だけを再送する。Portal 切断時も active data plane は維持し、
指数 backoff で再接続する。初回 snapshot が network と RDS の両方へ反映されるまでは ready にならない。

## 診断

Controller は JSON structured log を stdout へ出し、内部 status listener で次を提供する。

- `/healthz`: process が稼働していれば `200`。Portal/Envoy 同期とは独立。
- `/readyz`: ACK 済み ActiveSnapshot があり Envoy stream が接続中なら `200`、それ以外は `503`。
- readiness body: Portal/Envoy 接続、desired/active snapshot ID、RDS version、apply phase、bounded error、retry count。

後続 snapshot の apply failure 中も、last-known-good と Envoy が利用可能なら ready を維持する。apply error は failure
report の送信成功で消さず、次の apply 開始または active commit まで保持する。秘密、認証情報、任意 HTTP body は記録しない。

調査時は `nft list table inet isureal_router`、各 interface の `tc -s class/qdisc/filter show`、Envoy admin の
`config_dump` と Controller `/readyz` を同じ snapshot/RDS version で突き合わせる。

## 自動検証

`router/tests/e2e/run.sh` は固定 project `isureal-router-e2e` の専用資源だけを操作する。次を実コンテナで検証する。

1. Portal desired/applied と Router readiness の初期収束。
2. 4 Host、10か国、default、invalid/duplicate、unknown Host と upstream header。
3. HTTP same/cross delay と 1 Mbps cross rate の実測。
4. defaultCountry の無停止動的変更と変更後100要求、明示された国がデフォルト国に優先すること、server region 変更の拒否。
5. 12方向すべての peer route と cross-region peer transfer の rate。
6. Portal 再起動、Router/Envoy 再起動後の自動再収束。
7. Router namespace 停止中に12方向すべての新規 peer 接続が失敗し、復旧後に再度成功すること。

失敗時は `router/.e2e-artifacts/compose.log` を保存し、通常は success/failure/interrupt のいずれでも専用 volume/network/
container を cleanup する。`ROUTER_E2E_KEEP_ON_FAILURE=1` のときだけ失敗後の専用資源を調査用に保持する。

実行手順と設定値は [router/README.md](../router/README.md)、検証済み要件は
[traceability.md](../specs/003-router/validation/traceability.md) を参照する。
