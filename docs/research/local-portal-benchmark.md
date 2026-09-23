# ローカルの選手画面からベンチマークを実行する

実ポータルと webapp を手元で起動し、試験用チームの選手としてログインして、ブラウザのボタンからベンチマークを実行する手順です。
ポータルへのジョブ登録、supervisor による実ベンチの実行、結果の報告と画面表示を確認します。

## 前提と検証範囲

Docker を起動し、Docker Compose 2.24.4 以降を使用してください。
以下のコマンドはリポジトリルートで実行します。

この環境は Compose project `isureal-debug` を使い、通常の開発環境とは DB とネットワークを分離します。
対象インスタンスは3件登録されますが、すべて同じ webapp を指します。
3台構成や Router 経由の通信は検証対象外です。
ベンチマークの実行ごとに、この環境の webapp データが初期化されます。

## 1. 起動して試験用チームを登録する

```bash
make debug-up
make debug-register
```

`debug-up` は webapp、実ポータル、それぞれの worker、supervisor、DB、Redis をビルドまたは起動します。
`debug-register` は試験用チーム、選手、対象インスタンスを登録します。
出力の `contestant_id` を控えてください。
代理ログインには `team_id` ではなく `contestant_id` を使います。

登録コマンドは繰り返し実行できます。
コンテナを再作成した場合は、`make debug-register` を再実行して対象 IP を更新してから、ブラウザでベンチを投入してください。

| 用途 | URL |
|---|---|
| ポータル | http://localhost:18081/ |
| 運営ログイン | http://localhost:18081/admin/session/new |
| 選手への代理ログイン | http://localhost:18081/admin/impersonate |
| 選手用ベンチマーク画面 | http://localhost:18081/contestant/benchmark_jobs |
| webapp | http://localhost:15173/ |
| webapp API の readiness | http://localhost:18080/health/ready |

## 2. 試験用チームの選手としてログインする

1. [運営ログイン](http://localhost:18081/admin/session/new) を開きます。
2. ユーザー名 `admin`、パスワード `local-debug-password` でログインします。
3. [代理ログイン](http://localhost:18081/admin/impersonate) を開きます。
4. **Contestant の ID** 欄に、登録時の `contestant_id` を入力して **Submit** を押します。
5. [選手用ベンチマーク画面](http://localhost:18081/contestant/benchmark_jobs) を開きます。

ローカルでは運営の代理ログイン機能を使って、登録済みの選手セッションへ切り替えます。
GitHub や Discord の OAuth 設定は不要です。

## 3. 画面のボタンからベンチマークを投入する

1. 対象サーバーの選択欄で **1** を選びます。
2. **Enqueue** ボタンを押します。
3. 遷移先のジョブ詳細画面で、完了するまで状態を確認します。
4. 完了後、成否とスコアを確認します。

既定の負荷は2ユーザー、1サイクルです。
同じチームに待機中または実行中のジョブがあると、追加の投入は HTTP 422 になります。
先のジョブが完了してから再実行してください。

この手順では `make debug-bench` は不要です。
そのコマンドは運営 API からジョブを投入するため、選手画面のボタン操作を確認するときは使いません。

## 4. ポータルへの結果登録を確認する

ブラウザの詳細画面で結果を確認した後、必要に応じてターミナルでも最新ジョブを取得できます。

```bash
make debug-result
```

出力はポータルの運営 API から取得した JSON です。
最新ジョブが `FINISHED`、かつ `result.passed` が `true` なら、ベンチの成功と結果登録を確認できます。
非同期実行のため、まだ完了していなければ少し待って同じコマンドを再実行してください。

supervisor がポータルからジョブを受け取り、webapp に対してベンチを実行して結果を報告します。
ポータル側では `portal-worker` が完了処理とダッシュボード更新を行います。

## 通常の60秒負荷で実行する

```bash
DEBUG_BENCH_CYCLES=0 make debug-up
make debug-register
```

起動後は選手用ベンチマーク画面を再読み込みし、対象サーバーを選んで **Enqueue** を押します。
既定の1サイクルへ戻す場合は、`DEBUG_BENCH_CYCLES=1 make debug-up` と `make debug-register` を実行してください。

## 問題の切り分けと停止

ジョブが進まない場合は、サービスの状態とログを確認します。

```bash
docker compose -f deploy/local-debug/compose.yaml ps
make debug-logs
```

`debug-logs` はポータル、`portal-worker`、supervisor のログを表示します。
ベンチが失敗した場合は、`make debug-result` の結果と運営向けの実行情報も確認してください。

利用後は次のコマンドで停止します。
DB、画像、ベンチログの volume は保持されます。

```bash
make debug-down
```

ポート変更や API ごとの接続設定は、[ローカル結合環境の README](../../deploy/local-debug/README.md) を参照してください。

## 手順の確認範囲

この文書は Makefile、Compose 設定、登録 CLI、選手画面の実装に基づいています。
文書作成時点では、環境の起動やブラウザからの実走検証は行っていません。
