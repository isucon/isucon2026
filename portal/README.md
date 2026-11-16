# ISUCON2026 ポータル（Go rewrite）

`isucon14-portal`（Rails 6.1）のバックエンドを Go で書き直したもの。
計画は [`../docs/plan/portal-go-rewrite.md`](../docs/plan/portal-go-rewrite.md)、
要件は [`../docs/plan/portal-go-requirements.md`](../docs/plan/portal-go-requirements.md) を参照。
仕様の典拠は isucon14-portal @ `4c2b99a` の実装コード。
開催日は **2026年10月31日（土）**。
開始、凍結、終了時刻は未設定であり、確定後に Pulumi の `environment` に設定する。

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
| envcheck 3 本 | JSON / text/plain / 空ボディ、CheckerToken 認証、インスタンス登録の副作用 |
| ページ | SPA シェル（meta 8 個 + manifest.json 解決）、運営 3 画面、broadcast、/terms /rules /docs（競技中のみ）/site/sha |
| ジョブ | BenchmarkCompletion / MaintainBest / UpdateContestantDashboard（Redis 事前生成）/ Clarification 系 / Web Push 配信（410 で購読削除）/ SSH 鍵同期 / Discord 同期・ロール / EC2 同期 / SSH 一括実行 / LongRunningCheck / ClearContestData |
| 常駐 | `cmd/portal`（app）、`cmd/worker`（PostgreSQL ジョブキューの消費）、`cmd/bot`（discordgo: ロール同期・readme リアクション・support comm）、`cmd/notifyloop` |
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
│   ├── job/             # ジョブ 20 本（PostgreSQL キュー / in-process）
│   ├── session/         # Redis セッション（Rails 非互換 = クリーンカットオーバー）
│   ├── cache/           # dashboard-v2:* 事前生成キャッシュ（CT-5）
│   ├── cloudformation/  # CFn テンプレート（ERB → text/template）
│   ├── token/           # bypass / checker token
│   ├── external/        # slack / discord / github / webpush / aws / ssh
│   └── db/              # pgx 接続と初期スキーマ（schema.rb を PostgreSQL に移植、CT-8）
├── deploy/              # AWS ECS Fargate へのデプロイ: Dockerfile / Pulumi / ecspresso（deploy/README.md）
├── frontend/            # React SPA（isucon14-portal 由来。webpack + pnpm）
│   ├── app/javascript/  # SPA 本体（audience / contestant / admin / broadcast_view の 4 バンドル）
│   ├── sw/              # Service Worker（Web Push 受信、CT-4）
│   ├── proto/           # buf 生成の TypeScript バインディング（bufbuild/es）
│   └── public/          # 静的アセット + ビルド出力（packs/ は gitignore）
├── gen/pb/              # buf generate（Go）
├── proto/               # isucon14-portal から流用した proto 定義
└── rspec/               # HTTP 化した受け入れテスト
```

## フロントエンド

isucon14-portal の React SPA を `frontend/` にそのまま取り込んだ。
表示文字列と開催案内は ISUCON2026 向けに更新している。
ロゴは RGB 素材から書き出した SVG、favicon と端末用アイコンは同じ素材から生成した PNG / ICO を使用する。

```sh
make frontend   # pnpm install + production ビルド → frontend/public/packs + manifest.json
make server     # ビルド済みアセットを配信する状態で起動（http://localhost:8081/）
```

- シェルはパック名を `manifest.json` でハッシュ付きパスに解決し（CT-3）、
  `/packs/*` は `public,max-age=31536000` で配信する
- `sw.js`・アイコン・ロゴなど public 直下のファイルはルート未マッチ時のフォールバックで配信する
- TS バインディングの再生成は `buf generate`（`frontend/proto` に出力）

### アイコンの再生成

元の RGB Illustrator 素材は `frontend/materials/isucon2026-logo-rgb.ai` に保持する。
横組み（1 ページ目）を横長タイル、縦組み（3 ページ目）を正方形アイコンに使用する。
Poppler の `pdftocairo` と `uv` を用意し、リポジトリルートから実行する。

```sh
uv run portal/scripts/generate_icons.py
cd portal/frontend
pnpm test
```

ブラウザ用の `icon-*.png` と `favicon.ico` は、縦組み素材の末尾の「26」だけを切り出す。
`favicon.ico` は 16 / 32 / 48 px を含む。
Apple Touch Icon、Android 用アイコン、タイルはロゴ全体を使用する。
サイズ指定のない Apple Touch Icon は 180 px とする。

## ベンチマーカーの接続

ジョブの取得と結果報告は、同梱の [supervisor](../supervisor/README.md) が中継します。
Docker によるビルドと、ローカルのポータルおよび webapp への接続手順は同 README を参照してください。

## テスト

`tests/{contract,integration,concurrency}` と `testkit` で構成する。
CI は portal-unit-contract-build / portal-integration-migration / portal-concurrency / portal-rspec の 4 ジョブ。

```sh
make test              # 単体 + 契約テスト（DB 不要）: internal/... + tests/contract
make test-integration  # PostgreSQL + Redis: HTTP 越しの結合（bench 一巡・envcheck 形式・スキーマ再適用・ジョブキュー）
make test-concurrency  # PostgreSQL + Redis: 並行不変条件（DM-4 重複 enqueue / NF-3 払い出し排他 / DM-3 確定競合 / キュー排他）
make rspec             # HTTP 化 RSpec（受け入れの主戦力）
```

- `tests/contract` は DB なしで守れる契約を固定する: token 形式（Ruby 生成のゴールデントークン込み、FR-AUTH-7）、
  非 admin マスク（CT-6）、通知エンコード（CT-4）、CFn テンプレート（埋め込み token の検証まで）
- `tests/integration` / `tests/concurrency` は接続先の環境変数
  （`ISUXPORTAL_TEST_DATABASE_URL` / `ISUXPORTAL_TEST_REDIS_URL`）が未設定なら skip する（backend と同じ流儀）

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

## Django 版からの登録情報移行

同意記録、個人の登録属性、アイコンのコピー、招待方式と認証の切り替えは
[登録情報の移行手順](docs/registration-migration.md) を参照してください。
検証専用モードと、空の DB に取り込む `cmd/import-registration` を用意しています。

## 負荷試験用の履歴データ

8時間に1000チームが3〜5分間隔で実行した履歴を `go run ./cmd/loadfixture` で生成できます。
初回と各回の投入時刻をランダム化し、結果、ログ、通知、ランキング用の参照を含む SQL を出力します。
生成と投入の手順は [loadtest/README.md](loadtest/README.md) を参照してください。

## 運用

- 本番は AWS ECS Fargate（ARM64）に配置する。手順と構成は
  [`deploy/README.md`](deploy/README.md) を参照。
- DB は PostgreSQL（`ISUXPORTAL_DATABASE_URL`）。`PORTAL_AUTO_MIGRATE=1` で起動時に
  `internal/db/schema.sql` と未適用の `internal/db/migrations/*.sql` をトランザクション内で適用する。
  適用済みバージョンは `schema_migrations` に記録する。
  Fargate では `cmd/migrate` をサービス更新前に 1 回実行し、起動時の自動適用は無効にする
- 非同期ジョブは PostgreSQL の `jobs` テーブルに積み、`cmd/worker` が
  `FOR UPDATE SKIP LOCKED` で 1 件ずつ取り出して実行する。失敗は指数バックオフで 5 回まで再試行し、
  それ以降は `status = 'dead'` に落として残す（`SELECT * FROM jobs WHERE status = 'dead'` で確認）。
  テストモード（`PORTAL_TEST_MODE=1`）では投入時に同期実行する
- DB は Aurora PostgreSQL Serverless v2、キャッシュは ElastiCache Valkey を使う。
  選手環境は EC2、アイコン保存先は S3 を使う。
  外部連携は GitHub / Discord / Slack など。非同期ジョブに SQS は使わない
- セッションは Rails と互換しないため、切り替え時に全員のログインが切れる（計画書 §7）
- SPA アセットは `ISUXPORTAL_PUBLIC_DIR=frontend/public` /
  `ISUXPORTAL_PACKS_MANIFEST=frontend/public/packs/manifest.json` を使う。
  デプロイ時はイメージ内の配置に合わせて設定する。`/packs` は長期キャッシュで配信する

## isucon14-portal からの意図的な差分

- **MySQL → PostgreSQL、SQS → PostgreSQL キュー**: Rails へのロールバック余地を捨てる決定
  （2026-09-14）に伴い、DB を backend と同じ PostgreSQL に統一し、SQS を `jobs` テーブルで置き換えた。
  16 テーブルの列名・意味は isucon14 と同じ（`datetime(6)` → `timestamptz`、`tinyint(1)` → `boolean`、
  `auto_increment` → `bigserial`）。加えて DM-4 の部分一意インデックス
  （同一チームの pending/running は 1 件）と `survey_responses.team_id` の一意制約を DB 側でも持つ
- **単一開催（P-3）**: `ISUXPORTAL_FINAL` / promoted 分岐 / qualify・final の 2 系統ロールは実装しない。
  Redis キーの round セグメントも落とす（`dashboard-v2:{visibility}[:team-{id}]`）
- **clarification の投稿を復活**: isucon14 の最終ビルドは感想戦向けに投稿を 403 で封じていたが、
  要件 FR-C-4 に従い投稿を受け付ける
- **登録更新・辞退の競技開始後 403 を復活**（FR-REG-5/6。isucon14 はコメントアウトしていた）
- **重複 enqueue 防止を DB ロックで実装**（DM-4。Rails はアプリ層 COUNT のみで競合に弱かった）
- **辞退・失格チームの招待 URL を無効化**（isucon14 は withdrawn を見ておらず、辞退後のチームに参加して
  どこにも登録できなくなる経路があった）
- **GitHub の `.keys` フォールバックはユーザー名規則を検証**（isucon14 はハイフンを削っていたため
  `john-doe` が別人 `johndoe` の鍵を取り込み得た）
- **`POST /session` + `_method=delete` を受ける**（SPA のログアウトが Rails の MethodOverride 慣習で
  送るため。Go 側に override ミドルウェアを置いた）
- **ETag の値**は Rails とバイト一致しない（304 の挙動のみ互換）
- `EnvCheck.status` は Rails 同様 DONE 固定（感想戦 FIXME の踏襲）。段階表示を戻す場合は
  [Issue #16](https://github.com/isucon/isucon2026/issues/16) を参照
- 空の pending 払い出しで target が無い場合、bench receive は 500 ではなく空 IP を返す

## protobuf の再生成

```sh
buf generate                              # Go → gen/pb
buf generate --template buf.gen.ruby.yaml # Ruby（テスト用） → rspec/lib
```

## 利用言語とエージェントの調査

ベンチマーカーは初期化レスポンスの `language`、`agent`、`harness`、`model` を `SurveyResponse` で送信し、スーパーバイザーがポータルへ中継します。
申告は調査用で、得点や合否の判定には使いません。
`agent`、`harness`、`model` が未申告の場合は空文字で保存します。
初期化前など `language` が空の報告は、既存の申告を上書きしません。

`model` に複数のモデルを記録する場合は、`"model-a,model-b"` のようにカンマで区切ります。
`brain` は AI モデル未使用を表します。
未申告や不明の場合は空文字とし、`brain` と区別します。
申告文字列は分割や正規化をせず、最新値と履歴にそのまま保存します。

| テーブル | 保存単位 | 更新方法 |
| --- | --- | --- |
| `survey_responses` | チームごとに1行 | 最新の申告で上書き |
| `survey_response_history` | ベンチジョブごとに1行 | 同じジョブの途中報告と確定報告で更新。別ジョブの行は保持 |

履歴の `created_at` は最初の保存時刻、`updated_at` は最後の更新時刻です。
同じ組み合わせのまま次のベンチを実行した場合も、別の履歴行を保存します。
初期化が成功すれば、その後ベンチが失敗した場合も申告を保存します。
履歴はベンチ実行時の申告であり、実行と実行の間の編集履歴や、同じジョブ内の申告の変更過程は記録しません。
競技データのリセット時は、最新値と履歴の両方を削除します。

チームごとの最新の組み合わせを集計する例です。

```sql
SELECT language, agent, harness, model, COUNT(*) AS teams
FROM survey_responses
GROUP BY language, agent, harness, model
ORDER BY teams DESC;
```

この集計では `model-a,model-b` を1つの組み合わせとして扱います。
モデルごとの件数が必要な場合は `string_to_array(model, ',')` で分割して集計してください。

チームの変遷をベンチ実行順に確認する例です。
`$1` には対象のチーム ID を指定します。

```sql
SELECT benchmark_job_id, language, agent, harness, model, created_at, updated_at
FROM survey_response_history
WHERE team_id = $1
ORDER BY benchmark_job_id;
```

既存 DB には `005_survey_metadata.sql` と `006_survey_model.sql` を適用します。
`005` が適用済みの場合も、`006` が最新値と履歴の両方へ `model` 列を追加します。
移行時点の最新の申告を履歴へコピーし、過去の未収集の `agent`、`harness`、`model` は空文字で残します。
移行前に上書きされた申告は復元できません。
既存の運用手順どおり、ポータルのサービス更新前に `cmd/migrate` を実行し、ベンチマーカーを含むスーパーバイザーも更新してください。
古いスーパーバイザーは追加フィールドを中継時に破棄するため、ポータルだけの更新では収集できません。

## 当日サポート対象の管理

運営画面のチーム編集で「当日サポート外」を設定・解除できます。
環境チェック期限に間に合わなかったチームなどに運営が手動で設定するフラグです。
チーム一覧と詳細に表示され、参加資格・ログイン・順位には影響しません。
既存チームと新規チームの初期値は無効です。環境チェック結果からの自動設定や、既存の失格フラグの変更は行いません。
デプロイ前に通常の `cmd/migrate` を実行し、`007_team_onsite_support.sql` を適用してください。
