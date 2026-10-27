# 擬似地理分散 Router 設計

Router は、単一ホスト上の4台のサーバーを app1=AP、app2=EU、app3=US、db1=AP に配置したものとして扱い、
HTTP 通信とサーバー間通信の両方へ同じリージョン判定を適用する支援インフラである。Portal の desired config を
唯一の入力とし、Linux network data plane と HAProxy が同じ snapshot へ収束した後だけ applied として報告する。

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
                                           | status :18081      |
                   edge_net (internal)     | nftables / tc      |
        +--------+       HTTP :8080         |                    |
        | client |------------------------->|                    |
        +--------+                          +---------+----------+
                                                     ^  runtime API :18001
                                        shared network namespace
                                                     v
                                               +-----------+
                                               |  HAProxy  |
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
segment にある Router IP とする。HAProxy は別 container だが `network_mode: service:router` で Router の network
namespace を共有するため、固定 backend への通信も Router namespace から出る。AWS では同じホストの systemd service
（`isureal-router`、`isureal-haproxy`）として動く。

Compose の interface 名には依存しない。Controller は `172.31.1.2` などの設定済み local IPv4 を持つ interface を
実行時に検索し、重複または欠落を apply failure とする。

## 責務と権限境界

| Component | 主な責務 | 権限・到達範囲 |
|---|---|---|
| Portal | desired snapshot 配信、applied/failure 受理 | control network のみ |
| Router Controller | 検証、latest-wins reconcile、nft/tc、HAProxy の既定の国、診断 | `NET_ADMIN`。Docker socket と host network は持たない |
| HAProxy | Host/国別 routing、HTTP delay/rate | 非 root、追加 capability なし（AWS では 443 のための `CAP_NET_BIND_SERVICE` だけ）。Router namespace を共有 |
| app1〜3 | アプリを表す E2E fixture | 各専用 segment のみ。E2E では static route 設定用 `NET_ADMIN` のみ |
| db1 | DB ネットワーク検証用 fixture（E2E のみ HTTP を使用。本配備は PostgreSQL 16） | 専用 segment のみ。公開 HTTP cluster/Host は持たない |
| E2E runner | Connect/HTTP の外部観測と自動判定 | control/edge のみ。server segment と Docker socket へ接続しない |

全 Compose network は `internal: true` で、host port を公開しない。Router と fixture は `privileged` を使わず、
必要な capability を `NET_ADMIN` に限定する。Portal、HAProxy、runner は capability をすべて drop する。

## HTTP data plane

HAProxy 2.8 の設定は、Router の環境変数（Host 名、サーバーの IP と上流のポート、traffic profile、待ち受けと TLS
証明書）だけから `isureal-router haproxy-config` が `ROUTER_HAPROXY_DIR` に作る。HAProxy のサービスは起動のたびに
これを実行してから HAProxy を起動するので、設定は Controller と同じ環境から決まる。Portal の snapshot で変わるのは
`defaultCountry` だけなので、Controller は HAProxy の runtime API で既定の国の map を書き換え、HAProxy を再読み込み
しない。書き換えは次の要求から全接続に効く。

- app Host はクライアントの国に対応するリージョンへ送る。JP/KR/CN/AU は app1 (AP)、GB/DE/FR は app2 (EU)、US/CA/MX は app3 (US) を使う。
- app1〜3 Host は各固定 server へ送る。db1 は HTTP routing の対象にせず、db1 Host は 404 とする。
- Host は port と大文字小文字を無視して照合する。
- `X-Country-Code` は US/CA/MX、JP/KR/CN/AU、GB/DE/FR の10値だけを exact match する（カンマで分割しない）。
- header がない場合だけ Portal の `defaultCountry` を1値挿入する。最初の snapshot を反映するまでは既定の国の map が
  空なので、HAProxy はすべての要求に `503` を返す。
- 空値、未知値、小文字、複数値は `400`、未知 Host は `404` を HAProxy が直接返す。
- `X-Client-IP` は検証せずにそのまま転送し、header がない場合だけ接続元アドレスを挿入する。bench が宣言する値なので
  偽装対策はせず、HAProxy の CPU を使う検証も入れない。
- client country と target server の region が同じ route は same profile、異なる route はその region pair の profile を使う。
- delay は Lua の action（`core.msleep`。イベントループを止めない）で1要求に1回足す。ミリ秒単位に切り上げる。
- rate は `bwlim-in`（要求）と `bwlim-out`（応答）の filter で、Host と国の組ごとの bucket を stick-table で共有する。
  header のない要求は、国を明示した要求とは別の bucket を使う。
- タイムアウトは接続 2 秒、要求 header の受信 30 秒、上流の無応答 60 秒、要求中のクライアントの無通信 5 分、
  次の要求を待つ keep-alive 1 時間。上流への再試行はしない（接続できなければすぐ 503）。
- `/api/notifications`（SSE）だけは `option http-no-delay` を付けた backend（`app1_stream` など）に送る。HAProxy は
  終わっていない応答の header と途中のデータを MSG_MORE で送るので、これがないと購読のたびに header と各イベントが
  約 200 ms 遅れる（手元の計測で 205〜210 ms。option を付けると 0〜1 ms）。defaults には付けない。帯域制限の下の
  大きな応答で HAProxy の CPU が約 35% 増えるため。

Envoy（2026-10-27 まで使っていた）との違いで、意図して受け入れたもの:

- 要求と応答はそれぞれ route の上限まで使える。Envoy は1つの bucket を両方向で共有していた。
- 上流のタイムアウトは無通信の時間である。Envoy の route timeout は応答全体の時間だったので、60 秒を超える SSE や
  遅い大きな応答も、データが流れていれば切らない。
- パスの正規化（Envoy の `normalize_path`、`merge_slashes`）はしない。HAProxy 2.8 では実験的機能で、アプリの nginx が
  同じ正規化をする。
- Envoy が付けていた `x-request-id`、`x-envoy-*`、`server: envoy` を付けない。WebSocket などの Upgrade を通す。

## サーバー間 data plane
## サーバー間 data plane

Router は namespaced `net.ipv4.ip_forward=1` を確認し、専用 `inet isureal_router` table の forward chain を管理する。
許可するのは異なる4つの server interface 間の12方向と established/related reply だけで、edge/control interface は
forward allow に含めない。

各 destination interface の egress には HTB root を置き、US・AP・EU の class と leaf を作る。leaf は遅延のある class
では netem、遅延のない class（same profile と default class）では pfifo とする。遅延のない netem は FIFO と同じ
動作なので、より軽い pfifo で置き換えている。source server IP
の u32 filter で source region class を選び、source/destination server region の組から delay/rate を決める。
app1↔db1 の2方向は same profile、それ以外の10方向は AP-EU・EU-US・AP-US のいずれかの region pair profile を使う。
pair profile は双方向で共通である。

既存 child を持つ HTB root は in-place replace できないため、更新 transaction ごとに Router 専用 root qdisc を
削除して完全な tree を再構築する。途中失敗時は直前の candidate について同じ処理を行い、last-known-good を戻す。
nft batch は一括検査後に table 単位で反映する。tc の複数 interface 更新自体は kernel 上で原子的ではないため、
外部 commit と成功報告は全 interface と HAProxy が成功した後に限定し、失敗時 rollback で整合させる。

production default は次のとおりである。各値は Router の環境変数で個別に変更できる。

| Classification | Additional end-to-end delay | Bandwidth ceiling |
|---|---:|---:|
| same region | 0 ms | 1 Gbps |
| AP↔EU | 100 ms | 200 Mbps |
| EU↔US | 100 ms | 200 Mbps |
| AP↔US | 100 ms | 200 Mbps |

サーバー間の100 msはRTTであり、Routerは各方向のtcへ50 msずつ設定する。クライアントからのHTTP通信では、
HAProxyがリクエスト処理へ100 msを1回注入する。2026-10-26 に新しいベンチで計測し、200 ms から 100 ms に下げた
（200 ms では初期実装の海外ユーザーがほとんど投稿を終えられず、海外の登録が users のロックを長く握って asia の
1 日の始まりまで遅らせた）。

E2E は分類 code path を変えず、測定しやすい 0/120 ms と 100/1 Mbps へ値だけを縮尺変更する。

## 性能（2 vCPU に収める）

Router は c6in.large（2 vCPU）で動く。チームのサーバーは合わせて 10 vCPU に収めるので、Router の vCPU は
増やさず、処理を軽くして上位チームの負荷でも律速しないようにする。

2026-10-26 に、最上位帯の実装（team 7 の複製に JPEG のヘッダだけの検証を入れたもの、約 10 万点）で
同じ seed の走行を比べた。CPU 時間は負荷の中盤の 30 秒、1 GB はその間に ens5 と data0 が送受信した量。

| 構成 | CPU 平均 / p95 / 最大 | CPU 時間 / GB | パケット数（13 GB あたり） |
|---|---:|---:|---:|
| 変更前（data0 MTU 1500、割り込みの既定値） | 50% / 98% / 100% | 286 jiffies | 1,065 万 |
| data0 MTU 9001 | 39% / 80% / 88% | ― | ― |
| さらに NAPI の割り込みの見送り | 33% / 59〜71% / 69〜81% | 161〜176 jiffies | 440 万 |

採用した変更は次の 3 つで、遅延と帯域の意味は変えない。

- data0 の MTU を 9001 にする（`provisioning/aws/lib/user-data.ts`）。サーバーは VPC の既定の 9001 を
  使うが、DHCP を使わない data0 だけが 1500 だったので、サーバー間の転送とプロキシの上流が経路 MTU の探索で
  1500 に落ちていた。CPU は約 2 割減った。
- ENA の受信割り込みを 2 回まで見送り、200 µs ごとの NAPI のポーリングでまとめて処理する
  （`provisioning/ansible/roles/router/files/90-isucon-router-napi.rules`）。NET_RX の softirq が約 6 割減った。
- 遅延のない class の leaf を netem から pfifo にする（`router/internal/netcontrol/tc.go`）。

netem と pfifo の limit はパケット（skb）の数なので、ジャンボフレームと GRO でまとめた大きな skb が増えると、
同じ limit でもキューに入るバイト数が増える（計測では skb の平均が約 1.3 KB から約 4.9 KB になった）。地域間の
class（200 Mbps）が飽和したときは、パケットを落とす前の待ちがそのぶん長くなる。いまの最上位帯では地域間の
class は飽和していない。

試したが採用しなかったもの:

- conntrack を外す（raw の notrack）。1 GB あたりの CPU 時間が変わらなかった（167 jiffies）。
- （Envoy のとき）同じ地域の route の帯域制限フィルタを外す。Envoy の user 時間は 1 割ほど減ったが、全体ではばらつき
  （約 5%）の範囲だった。ベンチの HTTP はすべて同じ地域の route を通り、上限は国ごとの route に 1 Gbps ずつ
  なので、このフィルタは実際には効いていない。意味を変えないために残した（HAProxy でも同じ）。
- XDP。c6in.large の ENA では、ネイティブの XDP を付けるにはキューを最大の半分（1 本）に減らす必要があり、
  MTU も 3498 までになる（実機で `the Rx/Tx channel count should be at most half of the maximum allowed
  channel count`、`the current MTU (9001) is larger than the maximum allowed MTU (3498) while xdp is on`）。
  受信の 2 vCPU への分散とジャンボフレームの両方を失う。XDP は遅延も作れない（パケットを保持して後で送る
  仕組みがない）。
- eBPF の EDT（送信時刻を BPF で付けて fq に待たせる）と nftables の flowtable。プロファイルでは HTB、netem、
  u32 の分類は合わせて数 % で、置き換えの手間に見合わない。

### HTTP のプロキシ

カーネルを直した後の負荷の内訳は、Envoy の user と sys（HTTP の処理とソケット）が約半分、残りが softirq だった。
Envoy は 1 リクエストあたり user 約 0.24 ms、sys 約 0.25 ms を使っていた（team 7 の複製、4 万リクエスト）。
2026-10-27 に Envoy 1.32 を HAProxy 2.8 に替えた。同じ意味の設定（全 route の帯域制限、地域間の遅延、443 の TLS）で、
team 12 で交互に走らせた（seed 5、カーネルは上の採用後の設定）。

| アプリ（点数の目安） | プロキシ | 点数 | CPU 平均 / p95 / 最大 | CPU 時間 / GB | user |
|---|---|---:|---:|---:|---:|
| team 7 の複製（約 4.6 万点） | Envoy（3 回） | 45,162〜45,535 | 17〜18% / 41〜47% / 83〜93% | 199〜204 | 457〜483 |
| | HAProxy（3 回） | 45,676〜48,146 | 12% / 29〜36% / 43〜51% | 138〜140 | 148〜157 |
| ヘッダだけ検証する版（約 10 万点） | Envoy（2 回） | 92,747〜94,585 | 32% / 62〜63% / 66〜71% | 178〜180 | 902〜924 |
| | HAProxy（2 回） | 99,539〜99,718 | 22〜23% / 42〜43% / 58〜60% | 124〜125 | 254〜274 |
| | HAProxy（本番と同じ配備） | 96,386 | 22% / 43% / 59% | 123 | 253 |

user は負荷の中盤の 30 秒の user 時間（大半がプロキシ）。HAProxy は 1 GB あたりの CPU 時間を約 3 割、user 時間を
約 7 割減らし、p95 を約 3 分の 1 下げた。最上位帯では Envoy の回だけ、アプリへの要求のタイムアウト（1〜3 件）と
default class での破棄（後述）が出た。成長の上限（asia 約 1,460 人、いまの最上位帯の約 1.5 倍）でも、p95 は
6〜7 割に収まる見込みである。帯域制限や遅延を除いた試験用の設定では 113〜116 だったので、HTTP の意味を
すべて作るのに 1 割ほどかかっている。「本番と同じ配備」は、新しい Router と `isureal-haproxy.service`、新しい
環境変数と PEM を実機に入れたもの。経緯は `docs/plan/router-haproxy.md`。

ほかに効くものは次の 2 つである。

- ベンチから Router への経路。いまは bench のタスクが NAT gateway を経てパブリック IP へ送るので、ens5 は
  MSS 1448（MTU 1500）になる。supervisor の VPC とチームの VPC を VPC peering でつなぎ、Router のプライベート
  IP へ送れば、ens5 もジャンボフレームになる。NAT gateway の処理料金（最上位帯で走行 1 回に数 GB）も要らなくなる
  （計画: `docs/plan/bench-vpc-peering.md`）。
- 同じ地域の転送（app1↔db1）を Router から外す。転送するバイトの約 65% を占めるが、same profile の 1 Gbps の
  上限が EC2 の帯域に変わるので、問題の設定として判断が要る。

気づいた点: プロキシからアプリへの送信は server の組に当たらないので default class（1 Gbps）を全地域で共有する。
Envoy のときは上位帯の画像のアップロードでこの class が詰まり、パケットを落とすことがあった（HAProxy に替えた後の
最上位帯の 3 回では落ちていない）。いまの最上位帯ではゲート通過の時間は app1 の CPU で決まっており、上限を外しても
変わらなかった（2026-10-26 の計測）。ただし、3 台に負荷を分ける実装では、参加者から見えないこの上限が先に効く。宛先のアプリごとに class を分ける（地域ごとに 1 Gbps）か、
HTTP の帯域は HAProxy の route に任せて default class の上限を外すのがよい。

## Reconciliation と rollback

Controller は Portal stream reader、単一 apply worker、report retry loop を分離する。pending queue は容量1で、未処理の
中間 snapshot は最新値へ coalesce される。kernel と HAProxy を変更する writer は常に1つである。

```text
StreamDesired
  -> protobuf/domain 全体検証
  -> interface 解決 + nft/tc candidate 生成
  -> network candidate 適用・確認
  -> HAProxy の既定の国を runtime API で設定（set map、空なら add map）
  -> 読み戻して一致を確認
  -> ActiveSnapshot commit + ready
  -> ReportApplied(success)

network/HAProxy failure
  -> network と既定の国を last-known-good へ rollback
  -> ReportApplied(failure)
  -> bounded interval で retry（より新しい pending があればそちらを優先）

HAProxy の再起動（既定の国の map が空になり、要求は 503）
  -> apply worker が ROUTER_REPORT_RETRY_INTERVAL ごとに確認して検出（新しい snapshot の再試行中も）
  -> active snapshot の既定の国を書き戻す

report transport failure after commit
  -> data plane は維持
  -> report だけを再送
```

同じ snapshot ID と config の再受信は network を再適用せず、HAProxy が既定の国を使っていることを確かめて（再起動で
消えていれば書き戻して）から成功報告を再送する。Portal 切断時も active data plane は維持し、
指数 backoff で再接続する。初回 snapshot が network と HAProxy の両方へ反映されるまでは ready にならない。

## 診断

Controller は JSON structured log を stdout へ出し、内部 status listener で次を提供する。

- `/healthz`: process が稼働していれば `200`。Portal/HAProxy 同期とは独立。
- `/readyz`: ActiveSnapshot があり、要求のたびに HAProxy の runtime API へ問い合わせて既定の国が一致すれば `200`、
  それ以外は `503`。HAProxy を止めるとすぐ `503` になる。
- readiness body: Portal 接続、HAProxy が active snapshot を使っているか（`proxy_serving`、`proxy_error`）、
  desired/active snapshot ID、apply phase、bounded error、retry count。

後続 snapshot の apply failure 中も、last-known-good を HAProxy が使っていれば ready を維持する。apply error は failure
report の送信成功で消さず、次の apply 開始または active commit まで保持する。秘密、認証情報、任意 HTTP body は記録しない。

調査時は `nft list table inet isureal_router`、各 interface の `tc -s class/qdisc/filter show`、生成された
`$ROUTER_HAPROXY_DIR/haproxy.cfg`、runtime API（既定 `127.0.0.1:18001`）の `show map <dir>/default-country.map` と
`show stat`、Controller の `/readyz` を突き合わせる。

## 自動検証

`router/tests/e2e/run.sh` は固定 project `isureal-router-e2e` の専用資源だけを操作する。次を実コンテナで検証する。

1. Portal desired/applied と Router readiness の初期収束。
2. 4 Host、10か国、default、invalid/duplicate、unknown Host と upstream header（`X-Client-IP` の転送と挿入を含む）。
3. HTTP same/cross delay と 1 Mbps cross rate の実測。
4. defaultCountry の無停止動的変更と変更後100要求、明示された国がデフォルト国に優先すること、server region 変更の拒否。
5. 12方向すべての peer route と cross-region peer transfer の rate。
6. Portal 再起動、HAProxy だけの再起動（停止中は ready でないこと）、Router と HAProxy の再起動後の自動再収束。
7. Router namespace 停止中に12方向すべての新規 peer 接続が失敗し、復旧後に再度成功すること。

失敗時は `router/.e2e-artifacts/compose.log` を保存し、通常は success/failure/interrupt のいずれでも専用 volume/network/
container を cleanup する。`ROUTER_E2E_KEEP_ON_FAILURE=1` のときだけ失敗後の専用資源を調査用に保持する。

実行手順と設定値は [router/README.md](../router/README.md)、検証済み要件は
[traceability.md](../specs/003-router/validation/traceability.md) を参照する。
