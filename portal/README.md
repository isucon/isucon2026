# ISUCON2026 ポータル（Go rewrite）

`isucon14-portal`（Rails 6.1）のバックエンドを Go で書き直したもの。
計画は [`../docs/plan/portal-go-rewrite.md`](../docs/plan/portal-go-rewrite.md)、
要件は [`../docs/plan/portal-go-requirements.md`](../docs/plan/portal-go-requirements.md) を参照。
仕様の典拠は isucon14-portal @ `4c2b99a` の実装コード。

## 実装範囲

全 107 ルート + ジョブ + 常駐 4 サービスを実装済み。

| 領域 | 内容 |
|---|---|
| 認証・セッション | GitHub / Discord OAuth（アカウント差し替え FR-AUTH-3 含む）、Redis + 署名 Cookie セッション、bypass token（HMAC-SHA384、FR-AUTH-7）、運営ログイン・impersonate・`ISUXPORTAL_ADMIN_ONLY` |
| 参加登録 | 7 状態判定・チーム作成（AZ 割当）・招待参加（上限 3 人）・更新・辞退・S3 presign アイコン・クーポン |
| 選手 API 12 本 | ダッシュボード（キャッシュ + 304）、ベンチ enqueue/一覧/詳細、clarification、通知（after / read）、Web Push 購読、CFn テンプレート |
| 観客 API 3 本 | leaderboard・solo・チーム一覧 |
| 運営 API 26 本 | チーム CRUD・ジョブ絞り込み/ページング/代理 enqueue/キャンセル・clarification 回答/公開・env_check 閲覧・CFn・一括コマンド実行（変則ルート含む）・最終検証 3 種・dump_leaderboard・未整備統計 3 種 |
| bench API 4 本 | 行ロック払い出し・cancel_owned・途中報告（marked_at 順序制御）・確定報告（score センチネル CT-7） |
| envcheck 3 本 | JSON / text/plain / 空ボディ、CheckerToken + HMAC 署名の 2 方式、インスタンス登録の副作用 |
| ページ | SPA シェル（meta 8 個 + manifest.json 解決）、運営 3 画面、broadcast、/terms /rules /docs（競技中のみ）/site/sha |
| ジョブ | BenchmarkCompletion / MaintainBest / UpdateContestantDashboard（Redis 事前生成）/ Clarification 系 / Web Push 配信（410 で購読削除）/ SSH 鍵同期 / Discord 同期・ロール / EC2 同期 / SSH 一括実行 / LongRunningCheck / ClearContestData |
| 常駐 | `cmd/portal`（app）、`cmd/worker`（SQS）、`cmd/bot`（discordgo: ロール同期・readme リアクション・support comm）、`cmd/notifyloop` |
| 観測性 | slog / Sentry / Prometheus `/metrics`（NF-6）、graceful shutdown（NF-5） |

## ディレクトリ

```
portal/
├── cmd/{portal,worker,bot,notifyloop}/
├── internal/
│   ├── app/             # cmd 共有の依存初期化
│   ├── config/          # ISUXPORTAL_* 環境変数（CT-11）
│   ├── contest/         # 競技時刻判定・leaderboard エンジン ← 最重要
│   ├── model/           # 行 + to_pb 変換（マスク CT-6、状態遷移、DM-3/4）
│   ├── handler/         # 全ルート・ガード 4 種・CSRF・SPA シェル
│   ├── job/             # ジョブ 20 本（SQS / in-process）
│   ├── session/         # Redis セッション（Rails 非互換 = クリーンカットオーバー）
│   ├── cache/           # dashboard-v2:* 事前生成キャッシュ（CT-5）
│   ├── cloudformation/  # CFn テンプレート（ERB → text/template）
│   ├── token/           # bypass / checker token
│   ├── external/        # slack / discord / github / webpush / aws / ssh
│   └── db/              # 接続と初期スキーマ（schema.rb の写し、CT-8）
├── gen/pb/              # buf generate（Go）
├── proto/               # isucon14-portal から流用した proto 定義
└── rspec/               # HTTP 化した受け入れテスト（93 examples）
```

## テスト

backend/ と同じ構成（tests/{contract,integration,concurrency} + testkit）。
CI は portal-unit-contract-build / portal-integration-migration / portal-concurrency / portal-rspec の 4 ジョブ。

```sh
make test              # 単体 + 契約テスト（DB 不要）: internal/... + tests/contract
make test-integration  # MySQL + Redis: HTTP 越しの結合（bench 一巡・envcheck 形式・スキーマ再適用）
make test-concurrency  # MySQL + Redis: 並行不変条件（DM-4 重複 enqueue / NF-3 払い出し排他 / DM-3 確定競合）
make rspec             # HTTP 化 RSpec 93 examples（受け入れの主戦力）
```

- `tests/contract` は DB なしで守れる契約を固定する: token 形式（Ruby 生成のゴールデントークン込み、FR-AUTH-7）、
  非 admin マスク（CT-6）、通知エンコード（CT-4）、CFn テンプレート（埋め込み token の検証まで）
- `tests/integration` / `tests/concurrency` は接続先の環境変数
  （`ISUXPORTAL_TEST_DB_DSN` / `ISUXPORTAL_TEST_REDIS_URL`）が未設定なら skip する（backend と同じ流儀）

RSpec は計画 Phase 0 の方式で `contest_spec.rb` 465 行を HTTP 越しに転用したもの
（原本の `history: false` 欠落バグを修正済み）に加え、登録フロー（fake OAuth で
実フローを cookie 込みで通す）、bench 一巡、envcheck、admin API、通知、CSRF、
bypass token の Ruby ⇔ Go 相互運用などを検証する。

テストモード（`PORTAL_TEST_MODE=1`）限定のフック（本番では無効）:

| フック | 用途 |
|---|---|
| `X-Isux-Timing-*` / `X-Isux-Now` | 競技時刻・現在時刻の上書き（Rails の Time stub 相当） |
| `X-Isux-Session-{Contestant-Id,Team-Id,Staff}` | セッション注入 |
| `/debug/leaderboard` `/debug/update_best_scores` `/debug/update_contestant_dashboard` | leaderboard エンジン・ジョブの直接呼び出し |
| `/debug/oauth/*` | fake OAuth プロバイダ（authorize → callback を実フローで通す） |
| `/debug/flushdb` | example 間の Redis 初期化 |
| ジョブの同期実行 | perform_later が即時実行になる（副作用を決定的に検証するため） |

## 運用

- 本番は `ISUXPORTAL_SQS_QUEUE_URL` を設定すると perform_later が SQS 経由になり、
  `cmd/worker` が消費する。未設定ならプロセス内 goroutine で実行
- セッションは Rails と互換しないため、切り替え時に全員のログインが切れる（計画書 §7）
- Rails と並走する差分テスト期間は Redis の DB index を分けること（計画書 §3）
- SPA アセットは `ISUXPORTAL_PUBLIC_DIR` / `ISUXPORTAL_PACKS_MANIFEST` を isucon14-portal の
  `public/` に向ける（manifest.json でハッシュ付きパス解決、`/packs` は長期キャッシュ）

## isucon14-portal からの意図的な差分

- **単一開催（P-3）**: `ISUXPORTAL_FINAL` / promoted 分岐 / qualify・final の 2 系統ロールは実装しない。
  Redis キーの round セグメントも落とす（`dashboard-v2:{visibility}[:team-{id}]`）
- **clarification の投稿を復活**: isucon14 の最終ビルドは感想戦向けに投稿を 403 で封じていたが、
  要件 FR-C-4 に従い投稿を受け付ける
- **登録更新・辞退の競技開始後 403 を復活**（FR-REG-5/6。isucon14 はコメントアウトしていた）
- **重複 enqueue 防止を DB ロックで実装**（DM-4。Rails はアプリ層 COUNT のみで競合に弱かった）
- **ETag の値**は Rails とバイト一致しない（304 の挙動のみ互換）。差分テストでは ETag ヘッダを許容リストへ
- `EnvCheck.status` は Rails 同様 DONE 固定（感想戦 FIXME の踏襲）。段階表示を戻す場合は
  `internal/model/envcheck.go` のコメント参照
- 空の pending 払い出しで target が無い場合、bench receive は 500 ではなく空 IP を返す

## protobuf の再生成

```sh
buf generate                              # Go → gen/pb
buf generate --template buf.gen.ruby.yaml # Ruby（テスト用） → rspec/lib
```
