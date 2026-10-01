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
`make debug-up` は Python 3 で起動時刻の8時間前を競技開始、1時間後を競技終了に設定します。
凍結は終了と同時にし、起動後1時間はベンチを実行できます。
時刻は git 管理対象外の `deploy/local-debug/.env.timing` に保存し、次の `make debug-up` で更新します。
Timeline の横軸には終了後1時間の余白があるため、表示幅は10時間です。
固定の競技時刻を使う場合は `DEBUG_CONTEST_START`、`DEBUG_CONTEST_FREEZE`、`DEBUG_CONTEST_END` にタイムゾーン付きの日時を指定してください。

```sh
DEBUG_CONTEST_START=2026-09-25T09:00:00+09:00 \
DEBUG_CONTEST_FREEZE=2026-09-25T18:00:00+09:00 \
DEBUG_CONTEST_END=2026-09-25T18:00:00+09:00 make debug-up
```

`docker compose` を直接使う場合は `--env-file deploy/local-debug/.env.timing` を指定してください。
指定しない場合は、従来の2020年〜2099年の設定に戻ります。

## 履歴が蓄積した状態の表示

`make debug-up` の起動後に `make debug-loadfixture` を実行すると、この Compose 環境の `portal-db` に1000チームの履歴を追加します。
ホストに Go が必要です。
既存の試験チームと結果は保持し、1000001以降のIDを使います。
同じデータの再投入は拒否します。

```sh
make debug-up
make debug-loadfixture
# 生成済み履歴のスコア推移だけを更新する場合
make debug-refresh-scores
```

実行時点から過去8時間、3〜5分間隔の履歴を作成し、worker に全チームのダッシュボードキャッシュ更新を依頼します。
更新完了後に http://localhost:18081/ を再読み込みしてください。
選手の履歴は `/admin/impersonate` で contestant ID `1000001` に代理ログインして `/contestant/benchmark_jobs` を開くと確認できます。
生成したチームのインスタンスは架空のため、実ベンチの実行には従来の `make debug-register` のチームを使用してください。

`portal/compose.yaml` の DB（ホストの15432番ポート）へ投入したデータは、この環境には表示されません。
データ生成の詳細は [portal/loadtest/README.md](../../portal/loadtest/README.md) を参照してください。

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
