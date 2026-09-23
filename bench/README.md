# ISUReal ベンチマーカー（ベーシック版）

ISUReal アプリケーションに対して初期化 → 事前検証（pretest）→ 負荷走行（load）を実行し、
結果 JSON を標準出力へ出力する。仕様は `bench/spec-bench.md` を参照。

## 使い方

```console
$ go run . -target http://localhost:8080 -users 8
```

| フラグ | 既定値 | 説明 |
|---|---|---|
| `-target` | `http://localhost:8080` | 対象アプリのベース URL |
| `-host` | 空 | 接続先と公開ホスト名が異なる場合の HTTP Host ヘッダー |
| `-users` | `8` | 同時負荷ユーザー数 |
| `-cycles` | `0` | 0 より大きい場合、60 秒の負荷走行の代わりに指定サイクル数だけ実行する（開発用） |

終了コード: `0` = pass、`1` = fail、`2` = ベンチマーカー自身の異常。

## Vagrant 環境での実行

repository root で Vagrant 環境を起動した後、次を実行する。接続先と Router が受理する
Host ヘッダーは `VM_TOPOLOGY` から自動的に導出される。
ベンチマーカーは最初に `POST /api/v1/initialize` を実行するため、対象アプリのデータは初期化される。

```console
$ make vm-up
$ make vm-benchmark
```

開発中に 1 サイクルだけ実行する場合は、ベンチマーカーの引数を `BENCH_ARGS` で渡す。

```console
$ make vm-benchmark BENCH_ARGS='-users 2 -cycles 1'
```

負荷走行時間は **60 秒固定**（D-008、過去 ISUCON 全年度と同一。
`docs/research/isucon-benchmarker-termination.md` 参照）。60 秒走行のスコアは
締切までに完了するサイクル数が ±1 変わるため走行ごとに段階的にずれうる。
開発中に走行間でスコアを比較したい場合は `-cycles` でサイクル数を固定すること。

負荷走行中にエラー合計が **100 件**に達した時点で負荷走行を打ち切り、fail として
終了する（D-009、固定回数閾値方式）。閾値は較正段階で見直す。

## フェーズ構成

1. **initialize**: `POST /api/v1/initialize`（30 秒タイムアウト、INIT-10 暫定値）。
   応答の `base_time` を仮想時間の起点として取り込む（VT-003）。
2. **pretest**: 管理者ログイン、シードユーザーログイン、誤パスワード 401、
   登録と重複 409、`GET /me` 整合、投稿前フィード空（FR-012）を検査する。
   失敗は即 fail（失格相当）。
3. **load**: 投稿期間サイクルを実時間の許す限り繰り返す（spec-bench.md §3.3）。
   - 管理者 API で通知時刻を仮想現在 +3 秒に設定
   - ワーカーが投稿期間を開始するのをポーリングで待機
   - 通知の到達を確認（FR-002）
   - 全ユーザーが並列に JPEG を投稿 → 公開フィード閲覧 → RealMoji・コメント
   - 2 サイクル目以降は Memories に前日分が現れることを確認（FR-016）
   - `POST /api/v1/virtual-time/advance` で翌カレンダー日へ進め、
     ワーカーのオフセット同期を 2 秒待つ（D-006）

## スコア（暫定重み、較正前）

post=10 / realmoji=3 / comment=3 / feed=1 / notification=1 / memory=1、
エラー 1 件につき −10、下限 0。503 応答は仕様上再試行可能なため、
投稿系はリトライ（最大 4 回、線形バックオフ）後の失敗のみエラーとする。

出力 JSON の `breakdown` と `errors` は運営向け情報である（D-005: 選手向け開示は
pass と合計 score のみ）。

## ローカル E2E 実行手順

アプリ側は PostgreSQL + api + worker が必要。macOS でローカル実行する場合、
`ISUREAL_IMAGE_SANITIZER_MEMORY_BYTES` の既定値 512MiB は darwin の
`setrlimit(RLIMIT_AS)` で Go ランタイムが起動できず 503 になるため、
十分大きな値（例: `1099511627776`）を設定すること。

```console
$ go -C backend build -o /tmp/isureal/api ./cmd/api
$ go -C backend build -o /tmp/isureal/worker ./cmd/worker
$ go -C backend build -o /tmp/isureal/image-sanitizer ./cmd/image-sanitizer

$ export ISUREAL_DATABASE_URL='postgres://isureal:local-development-only@localhost:5432/isureal?sslmode=disable'
$ export ISUREAL_CURSOR_HMAC_KEY='local-e2e-cursor-key-0123456789abcdef'
$ export ISUREAL_IMAGE_ROOT=/tmp/isureal/images
$ export ISUREAL_IMAGE_SANITIZER_PATH=/tmp/isureal/image-sanitizer
$ export ISUREAL_MIGRATIONS_DIR=$PWD/backend/migrations
$ export ISUREAL_OPENAPI_PATH=$PWD/specs/001-isureal-social-app/contracts/openapi.yaml
$ /tmp/isureal/api & /tmp/isureal/worker &

$ go -C bench run . -target http://localhost:8080
```

`ISUREAL_MIGRATIONS_DIR` は `POST /api/v1/initialize` がスキーマリセットに使う
マイグレーションディレクトリ（未設定時はカレントディレクトリ相対の `migrations`）。

## supervisor からの実行

`ISUXBENCH_REPORT_FD` が渡された場合、ポータルと共有する `BenchmarkResult` の protobuf を専用パイプへ送信する。
起動時、初期化完了時、事前検証完了時、各負荷サイクル完了時に途中報告し、終了前に最終結果を送る。
途中報告の `passed` は false とし、正常完走した最終報告だけを true にする。
全体タイムアウトや SIGTERM による中断、負荷サイクルの未完了は失敗になる。
パイプへの書き込みは 5 秒でタイムアウトし、報告エラーは終了コード 2 とする。

```bash
# リポジトリルートからビルドする。生成 protobuf は ../portal を参照する。
go -C bench build -o /tmp/isureal-bench .

# 接続先と秘密値は systemd の EnvironmentFile 等で設定しておく。
# --target は supervisor がジョブから追加するため、ここでは指定しない。
ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT=150 \
  /usr/local/bin/isuxportal-supervisor /tmp/isureal-bench --target-port 8080
```

| 追加フラグ | 既定値 | 動作 |
|---|---|---|
| `-target-port` | `80` | ポートを含まない IP を `http://<IP>:<port>` に変換する際のポート。URL または明示ポート付きの対象には適用しない |
| `-timeout` | `2m` | 初期化から負荷終了までの全体上限。既存の 60 秒負荷は変更しない |
| `-only-post-validation` | `false` | 最終検証は未実装のため、対象へ接続せず失敗を報告して終了コード 1 で終了する |

`-target` の指定がなければ `ISUXBENCH_TARGET` を参照し、それもなければ従来の `http://localhost:8080` を使う。
`ISUXBENCH_ALL_ADDRESSES` は現状の単一対象シナリオでは使用しない。
負荷はサイクル境界で 60 秒経過を判定する既存仕様のため、最後のサイクルは 60 秒を超えて継続する場合がある。
全体上限の 120 秒でその継続も打ち切る。
本番の時間予算は実測して確定し、supervisor のハードタイムアウトはベンチの全体上限と報告処理の余裕を含めて長く設定する。
上記の 150 秒は初期設定例である。

supervisor 実行時の stdout は `pass` と `score` だけの JSON とする。
採点内訳、言語、詳細な失敗理由は stderr に出し、ポータルへ送る選手向けの失敗理由は一般的な文面にする。
単独実行時は従来どおり詳細 JSON を stdout に出す。
`rawScore` と `deduction` はそれぞれ重み付け後の加点と全エラーの減点で、合計点は下限 0 を適用した値を明示的に送る。

最終検証モードの検証内容は未定義であり、この対応だけでは本番の最終検証を実施できない。
初期化してから通常ベンチを回す代替動作は行わない。
残る supervisor 自体の修正と実行環境の準備は [結合要件](../docs/research/bench.md) を参照。

### 接続実装のテスト

```bash
go -C bench test -race ./...
go -C bench vet ./...
go -C bench build ./...
```

子プロセスへ実際に FD を引き継ぐテストで、protobuf の途中報告と確定、公開出力の制限、最終検証指定時に対象へ接続しないことを確認する。
フレーム長 65,535 / 65,536 バイトの境界、短い書き込み、不正 FD、書き込み失敗も検証する。
