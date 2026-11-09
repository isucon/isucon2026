# ISUCON2026 supervisor

ポータルからベンチジョブを取得し、子プロセスとして ISUReal ベンチを起動します。
ベンチの専用 FD から protobuf の途中結果を読み、終了時にログと終了コードを添えてポータルへ確定報告します。
流用元と変更点は [UPSTREAM.md](UPSTREAM.md) に記録しています。

```text
portal ← HTTP + protobuf → supervisor ← pipe + protobuf ← bench → webapp
```

## Docker でビルドする

リポジトリルートで実行します。
Rust をホストにインストールする必要はありません。

```bash
make -C supervisor docker-test
make -C supervisor docker-build
```

`isucon2026-supervisor:local` に supervisor と ISUReal ベンチが入ります。
既定の起動コマンドは `isuxportal-supervisor /usr/local/bin/isureal-bench --target-port 8080` です。
ベンチの実行対象はポータルのジョブから取得します。

### 投稿に使う写真のアーカイブを同梱する

ベンチは `-images`（環境変数 `ISUXBENCH_IMAGES`）に写真のアーカイブ（zip のパスか http(s) の URL）を取ります。
空なら生成した小さな画像を投稿するので、本番の条件とは違います。
ポータルから走る Supervisor には走行ごとに URL を渡す仕組みがないため、アーカイブをイメージに同梱します。

```bash
make supervisor-photos   # S3 の assets/merged.zip を var/photos/merged.zip に取る
docker build --platform linux/amd64 --build-arg PHOTOS=archive -f supervisor/Dockerfile -t isucon2026-supervisor:photos .
```

`PHOTOS=archive` で `var/photos/merged.zip` を `/opt/isucon/photos/merged.zip` に入れます。
既定の `PHOTOS=none` では同梱しないので、アーカイブがなくてもビルドできます（bench-runner はこちらを使い、走行ごとに署名付き URL を渡します）。
同梱したイメージでは、タスク定義の環境変数 `ISUXBENCH_IMAGES=/opt/isucon/photos/merged.zip` を supervisor に与えると、子プロセスのベンチがそれを読みます（Pulumi の `benchmarkImages`）。
ECR への push まで行うには、リポジトリルートで `make supervisor-image` を使います（[Supervisor構築手順](../provisioning/aws/supervisor/README.md)）。

## ホストでビルドする

Linux または macOS、Rust 1.90.0、Go（リポジトリ指定版）が必要です。
Linux では OpenSSL の開発ライブラリと pkg-config も必要です。
Docker のビルド環境はこれらを含みます。

```bash
make -C supervisor build
mkdir -p /tmp/isureal-supervisor
go -C bench build -o /tmp/isureal-supervisor/bench .
```

バイナリは `supervisor/target/release/isuxportal-supervisor` に出力されます。
Cargo.lock を使用するため、依存解決を更新せず `--locked` でビルドします。
Docker 内で生成された Linux 用バイナリは macOS では実行できません。

## ローカルのポータルと webapp に接続する

以下は既存の開発環境へ個別に接続する手順です。

### 接続先を用意する

構築済みの試験用 webapp を用意します。
ベンチは実行時に webapp のデータを初期化します。
実ポータルは次のコマンドで起動します。

```bash
make -C portal frontend
make -C portal server
```

ポータル画面は `http://localhost:8081/` です。
`make server` はテストモードで完了ジョブを同期実行します。
非同期の表示更新まで確認するときは、テストモードを無効にした `cmd/portal` と `cmd/worker` を、同じ DB、Redis、競技時刻設定で起動してください。
認証と競技時刻の設定は [portal/README.md](../portal/README.md) を参照してください。

ポータルには試験用チームと、number が 1〜3 の対象インスタンスを登録します。
ホスト実行では対象の `public_ipv4_address` に `127.0.0.1` を使えます。
3 件とも同じ IP にした場合は、単一 webapp への接続確認になります。
ポータルの試験データを自動登録する機能は、この supervisor には含めていません。

### ホストで supervisor を起動する

```bash
cp supervisor/.env.example supervisor/.env
# .env の TEAM_ID を登録した試験用チームの ID に、INSTANCE_NAME をその AZ に変更する。
# TOKEN はポータルの ISUXPORTAL_BENCH_TOKEN と一致させる。
set -a
. supervisor/.env
set +a
mkdir -p "$ISUXPORTAL_SUPERVISOR_LOG_DIRECTORY"

supervisor/target/release/isuxportal-supervisor \
  /tmp/isureal-supervisor/bench --target-port 8080 -users 2 -cycles 1
```

選手画面または運営画面から対象インスタンスを指定して enqueue します。
`pending → running → finished` の遷移と、ベンチのスコアがポータルに保存されることを確認します。
接続確認後は `-cycles 1` を外すと通常の60秒負荷になります。
採点失敗や実行異常では `errored` になる場合があるため、結果の pass と終了情報も確認してください。

## 設定

環境変数はすべて `ISUXPORTAL_SUPERVISOR_` を接頭辞に持ちます。

| 末尾 | 内容 | 既定値 |
|---|---|---|
| `ENDPOINT_URL` | ポータルの URL | 必須 |
| `TOKEN` | ポータルの `ISUXPORTAL_BENCH_TOKEN` | 必須 |
| `INSTANCE_NAME` | チームの `availability_zone` と一致する値 | 必須 |
| `TEAM_ID` | 試験対象のチーム ID。省略時はチーム制限なし | 未設定 |
| `HARD_TIMEOUT` | 子プロセスの時間上限、秒 | 210 |
| `LOG_DIRECTORY` | 書き込み可能なログ保存先 | `/tmp` |
| `INTERVAL_AFTER_EMPTY_RECEIVE` | 空キューの再確認間隔、秒 | 5 |

150秒はベンチの全体上限120秒を含む開発用の初期値です。
本番の時間予算は実測して確定してください。
HTTPS の証明書検証は有効で、Docker イメージは CA 証明書を含みます。
supervisor の環境変数は子プロセスのベンチにそのまま渡るので、ベンチの `ISUXBENCH_IMAGES`（写真のアーカイブ）などもタスク定義の環境変数で与えられます。

## 検証と残タスク

`make -C supervisor docker-test` はフォーマット検査、単体テスト、実子プロセスと HTTP の結合テスト、全ターゲットの型検査を実行します。
HTTP の相手はテスト用サーバーで、実ポータルの DB やランキングは検証しません。

- 正常結果の途中報告と確定、子プロセス終了後の FD 読み取り完了。
- 直前に pass を報告してから異常終了した場合の fail と0点への補正。
- 報告なしの終了、不正 protobuf、supervisor エラー時の成功判定の抑止。
- リクエストログからの job handle と出力ログの除去。

運用までの残タスクは [結合要件](../docs/research/bench.md) に集約しています。
特に次の制限があります。

- `cancel_owned` は自動実行しません。同じ AZ 名を複数実行機で共有するため、起動時に呼ぶと他の実行機のジョブを取り消す可能性があります。
- SIGTERM は実行中ジョブの終了を待ちます。強制終了時の子孫プロセス回収と孤児ジョブ回収は未整備です。
- 途中報告は15秒でタイムアウトし、失敗を記録して次へ進みます。確定報告は最大4回試行し、失敗するとプロセスが終了します。
- ログのローテーションと保存期限は外部で設定する必要があります。
- `--only-post-validation` はベンチ側で未実装です。対象を初期化せず失敗を報告します。

## 試し解き用AWS環境

ISUCON14のFargate設定を参考にしたチーム専用のIaCは、[Supervisor構築手順](../provisioning/aws/supervisor/README.md)を参照してください。
