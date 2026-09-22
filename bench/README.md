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
$ export ISUREAL_SCHEMA_PATH=$PWD/backend/schema/schema.sql
$ export ISUREAL_OPENAPI_PATH=$PWD/specs/001-isureal-social-app/contracts/openapi.yaml
$ /tmp/isureal/api & /tmp/isureal/worker &

$ go -C bench run . -target http://localhost:8080
```

`ISUREAL_SCHEMA_PATH` は `POST /api/v1/initialize` がスキーマリセットに使う
`schema.sql`（未設定時はカレントディレクトリ相対の `schema/schema.sql`）。
