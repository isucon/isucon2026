# ポータルと実ベンチのローカル結合環境

Docker Compose 2.24.4 以降を使用します（`!override` が必要です）。
リポジトリルートから次を実行します。

```bash
make debug-up        # webapp、実ポータル、両方のworker、supervisor、DB、Redisを起動
make debug-register  # 試験用チーム、選手、対象インスタンス3件を登録
make debug-bench     # IPを再取得して登録を更新し、実APIからenqueue
make debug-result    # 最新ジョブと結果を実APIから取得（JSON表示）
```

ベンチは非同期で実行されます。
`debug-result` が `FINISHED`、`result.passed: true` になれば通常走行の成功です。
既定値は2ユーザー、1サイクルです。
対象 webapp のデータは毎回初期化されます。

| 画面 | URL |
|---|---|
| ポータル | http://localhost:18081/ |
| 運営ログイン | http://localhost:18081/admin/session/new |
| 選手の代理ログイン | http://localhost:18081/admin/impersonate |
| webapp | http://localhost:15173/ |
| webapp API readiness | http://localhost:18080/health/ready |

運営ログインは `admin` / `local-debug-password` です。
選手画面を確認する場合、運営ログイン後に `/admin/impersonate` の Contestant の ID 欄へ、`debug-register` が返した `contestant_id` を入力して Submit し、`/` を開いてください。
GitHub や Discord の OAuth 設定は不要です。

登録は同じ選手識別子を使い、繰り返してもチームを増やしません。
コンテナの IPv4 をDNSから解決して、3件の対象インスタンスに設定します。
3台とも同じ webapp を指すため、これは単一 webapp の結合試験です。
`debug-bench` は実行前に登録を更新するため、コンテナ再作成によるIP変更にも追従します。
同じチームに pending/running のジョブがある場合、再enqueueは422になります。

```bash
make debug-logs      # portal、portal-worker、supervisorのログを表示
make debug-down      # 停止。DBと画像とベンチログのvolumeは保持

# 通常の60秒負荷へ切り替える
DEBUG_BENCH_CYCLES=0 make debug-up
make debug-bench

# ポートを変更する場合は、起動時に指定する
DEBUG_PORTAL_PORT=28081 DEBUG_API_PORT=28080 DEBUG_WEB_PORT=25173 make debug-up
```

Compose project は `isureal-debug` です。
通常の `isureal` や `portal` のDBとネットワークを共有しません。
`PORTAL_TEST_MODE=0` で実行するため、完了処理とダッシュボード更新は `portal-worker` の PostgreSQL ジョブキューを経由します。
競技時刻はこの環境専用の2020年開始、2099年終了に設定しています。

## 接続設定とAPIの対応

以下の JSON は protobuf のフィールドを説明するための表記です。
bench API と選手および運営の benchmark API は、実際には `Content-Type: application/vnd.google.protobuf` のバイナリを送受信します。
JSONをそのままcurlでPOSTする形式ではありません。
`local-debug` コマンドは生成済みGo protobufを使ってシリアライズします。

### 1. チームと対象インスタンスの登録

必要な登録データは、チームID、チームの `availability_zone`、number 1〜3 のインスタンス、それぞれの `public_ipv4_address` です。
この環境では登録コマンドがDBへトランザクションで投入します。
これはローカルCLIであり、新しい公開デバッグAPIは追加していません。
コマンドは `PORTAL_LOCAL_DEBUG=1` の明示設定がないと起動できません。

既存APIによるインスタンス登録は `POST /api/env_checks` の副作用を利用します。
`application/x-www-form-urlencoded` で、署名済みCheckerToken、`name=contest1`（またはcontest2、contest3）、`ip_address=<supervisorから到達できるIPv4>`、`passed=true` を送ります。
チームは先に存在する必要があります。
CheckerTokenはチームIDを含み、ポータルの `ISUXPORTAL_CHECKER_SECRET` と同じ鍵で署名します。
ベンチ用共有トークンとは別物です。

```text
POST /api/env_checks
 token=<CheckerToken>&name=contest1&ip_address=<IPv4>&passed=true
```

チームのAZは通常の参加登録時にサーバーが割り当てます。
現状の運営チーム更新APIはAZを更新しません。
この環境では登録コマンドが `apne1-az1` を設定し、supervisorの `instanceName` と一致させます。

### 2. ジョブをenqueueする

運営APIは `POST /api/admin/benchmark_jobs` です。
通常の運営セッションCookieと `X-CSRF-Token` が必要です。
`debug-bench` はローカルRedisに一時的な運営セッションを作成して、このAPIを呼びます。
HTTPの認証とCSRF検証は省略しません。

```json
{"teamId":"1","targetNumber":"1"}
```

選手からは `POST /api/contestant/benchmark_jobs` に `{"targetId":"<インスタンスのDB ID>"}` を送ります。
こちらは選手セッションとCSRFトークンを使い、チームIDをセッションから決定します。
運営の `targetNumber` と選手の `targetId` は別の値です。

### 3. supervisorがジョブを取得する

supervisor の `ISUXPORTAL_SUPERVISOR_ENDPOINT_URL` が、ポータルのベースURLです。
このComposeでは `http://portal:8081` を使います。
ホスト向けの18081番ではなく、コンテナ間の8081番です。

```text
POST /api/bench/receive
```

```json
{"token":"local-debug-bench-token","instanceName":"apne1-az1","teamId":"0"}
```

| APIフィールド | supervisor設定 | ポータルとの一致条件 |
|---|---|---|
| `token` | `ISUXPORTAL_SUPERVISOR_TOKEN` | `ISUXPORTAL_BENCH_TOKEN` と一致 |
| `instanceName` | `ISUXPORTAL_SUPERVISOR_INSTANCE_NAME` | enqueue時にジョブへ保存されるチームのAZと一致 |
| `teamId` | `ISUXPORTAL_SUPERVISOR_TEAM_ID` | 任意の絞り込み。未指定または0は全チーム |

この環境では他環境とDBが分離されているため、teamIdの絞り込みは設定していません。
応答の `jobHandle` に `jobId`、`handle`、`targetIpv4Address`、`allIpv4Addresses`、`extraCommandArgs` が入ります。
待機ジョブがないときは `jobHandle` がありません。

### 4. supervisorがベンチを起動する

`targetIpv4Address` を `--target` と `ISUXBENCH_TARGET` に渡し、3件のIPを `ISUXBENCH_ALL_ADDRESSES` に渡します。
この環境の `--target-port 8080` によって、対象は `http://<APIコンテナのIPv4>:8080` になります。
この段階にポータルの公開URLやブラウザ用ポートは使用しません。
報告用のパイプと `ISUXBENCH_REPORT_FD` はsupervisorが作成します。

### 5. 途中結果と確定結果を報告する

```text
POST /api/bench/benchmark_results
POST /api/bench/benchmark_results/completion
```

どちらも `jobId`、取得時に返された `handle`、`result` を送ります。
この認証にはジョブごとのhandleを使うため、共有tokenを再送する必要はありません。
途中結果は `finished=false` で `execution` を含めません。
確定結果は `finished=true` とし、pass、score、markedAt、終了コード、ログを送ります。
これらはsupervisorが組み立てるため、利用者が設定ファイルへ書く値ではありません。

結果取得は `GET /api/admin/benchmark_jobs/:id`（運営セッション）または `GET /api/contestant/benchmark_jobs/:id`（所属選手セッション）です。
`debug-result` は運営APIを使うため、stderrなどの運営向け情報も表示します。

## 検証範囲

登録コマンドのテストは `portal/cmd/local-debug/main_test.go` にあります。
DBとRedisの環境変数を設定して実行してください。
テスト用DBはテーブルを初期化するため、起動中ポータルのDBとは別に用意します。

```bash
go -C portal test ./cmd/local-debug -count=1
```

`ISUXPORTAL_TEST_DATABASE_URL` と `ISUXPORTAL_TEST_REDIS_URL` が未設定のときは、DBを使うテストをskipします。
CIの portal-integration-migration ジョブでも実行します。

最終検証モード、supervisorの障害復旧、3台構成やRouter経由の検証はこの環境の対象外です。
