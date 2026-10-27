# Router の HTTP data plane を HAProxy に置き換える

2026-10-27 に実施した。Router の Envoy 1.32 を HAProxy 2.8 に替え、xDS をやめた。HTTP の意味（docs/router.md の
「HTTP data plane」）、サーバー間の tc と nftables、Portal の desired config と applied の報告、readiness の契約は
変えていない。Envoy との細かな違いは docs/router.md に書いた。

## 実測

team 12、seed 5、遅延 100 ms で、Envoy と HAProxy を交互に走らせた。カーネルは docs/router.md の「性能」で採用した
設定（data0 の MTU 9001、NAPI の割り込みの見送り、遅延のない class は pfifo）。HAProxy の設定は実装が生成する
ものと同じで、全 route の帯域制限、地域間の遅延、443 の TLS を含む。CPU 時間は負荷の中盤の 30 秒の busy jiffies を、
その間に ens5 と data0 が送受信した量で割ったもの。user はその 30 秒の user 時間で、大半がプロキシである。

| アプリ（点数の目安） | プロキシ | 点数 | Router CPU 平均 / p95 / 最大 | CPU 時間 / GB | user |
|---|---|---:|---:|---:|---:|
| team 7 の複製（約 4.6 万点） | Envoy（3 回） | 45,162〜45,535 | 17〜18% / 41〜47% / 83〜93% | 199〜204 | 457〜483 |
| | HAProxy（3 回） | 45,676〜48,146 | 12% / 29〜36% / 43〜51% | 138〜140 | 148〜157 |
| ヘッダだけ検証する版（約 10 万点） | Envoy（2 回） | 92,747〜94,585 | 32% / 62〜63% / 66〜71% | 178〜180 | 902〜924 |
| | HAProxy（2 回） | 99,539〜99,718 | 22〜23% / 42〜43% / 58〜60% | 124〜125 | 254〜274 |
| | HAProxy（本番と同じ配備） | 96,386 | 22% / 43% / 59% | 123 | 253 |

HAProxy は 1 GB あたりの CPU 時間を約 3 割、user 時間を約 7 割減らし、最上位帯の p95 を約 3 分の 1 下げた。点数は
アプリの CPU で決まるので、低い負荷では同じだった。最上位帯では Envoy の回だけアプリへの要求のタイムアウト（1〜3 件）
と、プロキシからアプリへの default class での破棄（121〜1,762 パケット）が出た。帯域制限と遅延を除いた試験用の
設定（2026-10-26）では 113〜116 だったので、HTTP の意味をすべて作るのに 1 割ほどかかっている。

## 作ったもの

- `router/internal/haproxy`: Router の環境変数から HAProxy の設定を作る処理（`config.go`）と、runtime API で既定の
  国を書き換える処理（`runtime.go`）。
- `isureal-router haproxy-config`: 設定を `ROUTER_HAPROXY_DIR` に書くサブコマンド。HAProxy の service が起動の
  たびに実行する。
- Controller: xDS の代わりに既定の国を runtime API で設定し、読み戻して確かめる。HAProxy が再起動して既定の国を
  失ったら、`ROUTER_REPORT_RETRY_INTERVAL` ごとの確認で書き戻す。`/readyz` は要求ごとに HAProxy に問い合わせる。
- AMI（`provisioning/ansible/roles/router`）: Envoy の代わりに Ubuntu 24.04 の haproxy パッケージ（2.8）と
  `isureal-haproxy.service`。パッケージの `haproxy.service` は mask する。
- user-data（`provisioning/aws/lib/user-data.ts`）: Envoy の bootstrap の代わりに、証明書と鍵をまとめた PEM と、
  Router の環境変数（HTTPS の待ち受け、証明書、上流のポート）。
- compose（`router/compose.e2e.yaml`、`compose.yaml`）: Envoy のコンテナを `router/Dockerfile` の `haproxy` target
  （`haproxy:2.8.16-alpine` に Router のバイナリを足したもの）に替えた。
- bench-runner: Envoy の統計の代わりに HAProxy の `show stat` の増分を出す。

## 計画から変えたこと

- snapshot ごとに設定を作り直して reload するつもりだったが、既定の国だけを runtime API の map で書き換える形に
  した。reload では古い worker に残った keep-alive の接続が、古い既定の国のまま次の要求を処理しうる。map なら
  次の要求から全接続に効く。
- 既定の国を proc 変数に入れる案（CLI の `set var`）は、HAProxy 2.8 では実験的機能なのでやめた。
- パスの正規化（`normalize-uri`）も HAProxy 2.8 では実験的機能なので入れていない。アプリの nginx が同じ正規化をする。

## 確かめたこと

- Go の単体テスト（設定の生成、runtime API、Controller の rollback と HAProxy の再起動からの復帰、readiness）。
- ルーターの E2E（`router/tests/e2e/run.sh`）: 全項目が通った。HTTP の遅延は 122 ms（設定 120 ms）、1 Mbps の
  route で 384 KiB が 2.9 秒。既定の国の変更は 0.2 秒で反映され、直後の 100 要求も新しい国になった。HAProxy だけを
  止めると `/readyz` は 503 になり、起動し直すと復帰した。
- 開発用の compose（隔離した project）の smoke: 3 地域の app Host で登録でき、地域間の DB への RTT は 100 ms。
- team 12 の実機に、新しい Router のバイナリ、`isureal-haproxy.service`、user-data と同じ環境変数と PEM を入れた。
  HTTP と HTTPS が通り、Portal で既定の国を JP → US → JP と変えると 0.2 秒ほどで反映された。HAProxy を再起動すると
  `/readyz` が 503 になり、約 0.4 秒で既定の国が書き戻されて 200 に戻った。最上位帯の負荷で 1 回走らせ（96,386 点、
  エラー 0）、`show errors` に記録された不正な要求は 0 件だった（`ereq` の 170 件はクライアントの途中終了）。
  確認の後は Envoy の構成に戻した。

## 配備

- 新しい Router の AMI（`make ami ROLE=router`）と新しい user-data は組にして入れる。いまの AMI（Envoy）に新しい
  user-data を当てても HAProxy は起動しない。Pulumi の `routerAmiId` と `teamImages` の Router の AMI を新しいものに
  替え、Router を作り直す。EIP でない公開 IP は変わる。
