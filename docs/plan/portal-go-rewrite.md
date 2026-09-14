# ポータルバックエンド Go rewrite 実装計画

作成日: 2026-08-23。調査結果は [`../survey/portal/`](../survey/portal/README.md)、
要件定義は [`portal-go-requirements.md`](portal-go-requirements.md) を参照（食い違う場合は要件定義が優先）。

`isucon14-portal`（Rails 6.1.6）のバックエンドを Go で書き直し、
フロントエンド・supervisor・envcheck・インフラはそのまま流用する。

> **2026-09-14 追記（決定）**: Rails へのロールバック余地は持たない。
> これに伴い基本方針 2・4 を改め、DB は **PostgreSQL**（backend と同じ）、
> 非同期ジョブは **SQS ではなく PostgreSQL の `jobs` テーブル**で実装した。
> Rails と同じ DB を見る前提の差分テスト（§6 層 2）と §7 のロールバック手順は廃止。
> 受け入れは HTTP 化 RSpec（Go にのみ向ける）と Go のテスト 3 層で行う。
> スキーマは 16 テーブルの列名・意味を維持したうえで PostgreSQL の型に移植し
> （`datetime(6)` → `timestamptz`、`tinyint(1)` → `boolean`、`auto_increment` → `bigserial`）、
> DM-4 の部分一意インデックスと `survey_responses.team_id` の一意制約を追加した。
> 詳細は [`portal/README.md`](../../portal/README.md) の「運用」「意図的な差分」を参照。

## 0. 前提と方針

### 確定している前提

| # | 前提 |
|---|---|
| 1 | ポータルのホスティングは **AWS**。さくらへの移設は要件ではない |
| 2 | 選手の競技環境は **AWS**（EC2 + CloudFormation）。1 チーム 3 台 |
| 3 | **supervisor は Rust のまま**。手書き 947 行、依存も薄い |
| 4 | 既存の登録データ（`isucon2026-portal`）は移行する |
| 5 | 競技日は **2026-11-25**。登録締切は **2026-09-30** |
| 6 | **予選は行わない。単一開催（本選のみ）として実装する**（§11.4） |

### 基本方針

1. **契約を先に凍結し、実装をそれに合わせる。**
   protobuf・非 protobuf 3 本・meta タグ・DB スキーマは既存のまま維持する
2. **DB スキーマは `isucon14-portal` の 16 テーブルをそのまま使う。**
   Rails → Go でデータ移行を発生させない（`isucon2026-portal` からの移行は別勘定）
3. **既存の `contest_spec.rb` 465 行を HTTP 越しに転用し、受け入れ条件にする。**
   leaderboard の凍結 × 可視性 3 系統はここでテストされている。
   加えて Rails と Go を同じ DB に向けて並べ、全エンドポイントのレスポンスを突き合わせる
4. **SQS と ecspresso は維持する。**
   Terraform / taskdef / sops をそのまま使い、app コンテナの中身だけ差し替える

### スコープ

| 書き直す | 残す |
|---|---|
| `app/controllers` 1,614 行 | React SPA 9,957 行 |
| `app/models` 1,232 行 | supervisor（Rust 947 行 + 生成 1,576 行） |
| `app/jobs` 622 行 | envcheck（Go） |
| `app/views` 241 行（実ページ 3 枚） | benchrun（Go） |
| `config/*` 909 行 | proto 定義 46 ファイル |
| `bin/isuxportal-discord-bot` 210 行 | Terraform 24 ファイル 1,634 行 |
| `lib/monkey_patches` 28 行 | ecspresso / jsonnet / sops 16 ファイル |
| **計 4,858 行** | |

想定する Go 実装は **8,200〜11,600 行**。

---

## 1. 技術選定

| 領域 | 採用 | 理由 |
|---|---|---|
| ルータ | `go-chi/chi/v5` | ミドルウェアの合成が素直。変則ルート `/:request_id/:id/output` も書ける。標準 `ServeMux` でも可能だが、ガード 4 種の合成で chi のほうが読みやすい |
| DB | `database/sql` + `jackc/pgx/v5/stdlib` | 2026-09-14 に MySQL から PostgreSQL へ変更（冒頭の追記参照） |
| クエリ | `sqlc` + 一部手書き | 定型は sqlc で型付け生成。**leaderboard だけは `sql.Rows` を直接回す**（ストリーミングが必要） |
| protobuf | `google.golang.org/protobuf` | `buf` で生成。既存の `buf.gen.yaml` に Go プラグインがすでにある |
| セッション | Redis（`redis/go-redis/v9`）+ 署名付き Cookie | Rails 互換は狙わない（後述）。Cookie は `gorilla/securecookie` 相当で十分 |
| 非同期ジョブ | PostgreSQL の `jobs` テーブル（`FOR UPDATE SKIP LOCKED`） | 2026-09-14 に SQS から変更。マネージドサービスを 1 つ減らし、ローカル・CI で本番同等の経路をテストできる |
| OAuth | `golang.org/x/oauth2` | GitHub は `oauth2/github`。Discord は endpoint を自前で定義 |
| Discord bot | `bwmarrin/discordgo` | |
| Web Push | `SherClockHolmes/webpush-go` | VAPID 対応。**実機検証を Phase 6 で必ず行う** |
| S3 presigned | `aws-sdk-go-v2/service/s3` の `Presigner` | アイコンアップロード用 |
| EC2 | `aws-sdk-go-v2/service/ec2` | インスタンス状態同期 |
| SSH | `golang.org/x/crypto/ssh` | 一括コマンド実行（`net-ssh` の代替） |
| テンプレート | `html/template` | 運営 3 画面 + SPA シェル。合計 4 枚 |
| マイグレーション | `pressly/goose` | 既存スキーマを初期マイグレーションとして取り込む |
| ログ | `log/slog` | |
| エラー通知 | `getsentry/sentry-go` | 既存が Sentry |
| メトリクス | `prometheus/client_golang` | New Relic からの乗り換え。ALB のヘルスチェックとは別 |
| 設定 | 環境変数を構造体に読む薄い自前実装 | 既存の `ISUXPORTAL_*` 30 個超をそのまま使う |

### セッションは Rails 互換を狙わない

Rails 6.1 + `redis-actionpack` はセッション本体を **Ruby の Marshal 形式**で Redis に置く。
Go から読むのは現実的でない。

したがって **段階移行（URL ごとに Rails と Go を振り分ける）はできない**。
クリーンカットオーバーになり、切り替え時に**全員のログインが切れる**。
これは受け入れる。切り替えは登録締切後、競技日までの間に行う（§7）。

セッションに載せる値は 5 つ。Rails と同じ意味論を保つ。

```go
type Session struct {
    ContestantID  int64            // 選手
    Staff         bool             // 運営
    BypassToken   string           // 署名付きトークン
    GitHubLogin   *OAuthIdentity   // 登録前の「ログインしたがチームが無い」状態
    DiscordLogin  *OAuthIdentity   // 同上
    BackTo        string           // 認証後の戻り先
}
```

---

## 2. リポジトリ構成

```
portal/
├── cmd/
│   ├── portal/          # HTTP サーバ（app タスク）
│   ├── worker/          # SQS コンシューマ（worker タスク）
│   ├── bot/             # Discord bot（bot タスク）
│   └── notifyloop/      # 滞留監視ループ（notifyloop タスク）
├── internal/
│   ├── config/          # 環境変数 → 構造体
│   ├── db/
│   │   ├── sqlc/        # sqlc 生成
│   │   └── query/       # *.sql
│   ├── domain/
│   │   ├── contest/     # 競技時刻の判定・leaderboard  ← 最重要
│   │   ├── benchmark/   # ジョブの状態遷移
│   │   └── team/
│   ├── handler/
│   │   ├── api/         # protobuf 54 本
│   │   ├── admin/       # ERB 相当 3 画面 + フォーム POST
│   │   ├── bench/       # supervisor 向け 4 本
│   │   ├── envcheck/    # 非 protobuf 3 本
│   │   └── page/        # SPA シェル
│   ├── middleware/      # ガード 4 種 + 全体スイッチ
│   ├── session/
│   ├── cache/           # Redis（ダッシュボード事前生成）
│   ├── job/             # ジョブ 20 本
│   └── external/        # github / discord / slack / webpush / aws / ssh
├── gen/pb/              # buf 生成（Go）
├── templates/           # 4 枚
├── db/migrations/
└── static/
```

`buf.gen.yaml` は既存を流用し、2 箇所だけ変更する。

```yaml
managed:
  override:
    - file_option: go_package_prefix
      value: github.com/isucon/isucon2026/portal/gen/pb   # 変更
plugins:
  - remote: buf.build/protocolbuffers/go
    out: portal/gen/pb                                     # 変更
  - remote: buf.build/bufbuild/es:v2.0.0
    out: portal/proto                                      # TS（据え置き）
  - remote: buf.build/community/neoeinstein-prost:v0.4.0
    out: supervisor/gen                                    # Rust（据え置き）
```

Ruby 向けの 2 プラグインは削除する。`buf generate` 一発で Go・TS・Rust が揃う。

---

## 3. 守る契約

移植で最も落としやすい部分。**着手前にテストで固定する。**

| 契約 | 内容 |
|---|---|
| protobuf wire format | 54 本。`Content-Type: application/protobuf; proto=<full message name>` |
| エラーの protobuf | `isuxportal.proto.Error` に `human_message`（1 行）と `human_descriptions`（複数行） |
| 非 protobuf 3 本 | `GET /api/env_check_info` → **JSON**、`GET /api/ssh_public_keys` → **text/plain**、`POST /api/env_checks` → **空ボディ** |
| envcheck の認証 2 方式 | `CheckerToken`（期限付き）と旧方式の **HMAC-SHA384 署名**。両方実装する |
| SPA シェルの meta タグ **8 個** | `isux:*` 6 種 + **`csrf-param` / `csrf-token`**（`csrf_meta_tags` 相当） |
| CSRF トークン | **ApiClient が全 mutating リクエストに `X-Csrf-Token` を送る**（`ApiClient.ts:327`）。Go は発行・検証する。bench / envcheck は対象外 |
| アセット解決 | パック名は **`public/packs/manifest.json`** でハッシュ付きファイル名に解決（simpacker 互換）。長期キャッシュヘッダ維持 |
| Web Push ペイロード | **base64(Notification の protobuf バイナリ)**。sw.ts が `atob` → decode（`sw.ts:142`）。`encoded_message` カラムも同形式 |
| DB スキーマ 16 テーブル | **既存カラム・インデックスの変更禁止。追加は可**（同意記録 4 カラムはデータ移行で追加。Rails は未知カラムを無視するので差分テストと両立） |
| Redis キャッシュキー | isucon14 は `dashboard-v2:{round}:{visibility}[:team-{id}]` / `leaderboard-v2:{round}:admin`。**単一開催なので Go は `round` を落とす**（`dashboard-v2:{visibility}[:team-{id}]`）。レスポンスには現れないので差分テストには影響しないが、**Rails と並走する期間は Redis の DB index を分ける** |
| 変則ルート | `GET /api/admin/instance_command_execute_requests/:request_id/:id/output` |
| UA 弾き | `/session/new/*` で Discordbot / Slackbot に 404 |
| CSRF | bench と envcheck 系は検証しない（共有トークンと署名で認証） |

---

## 4. Phase 別計画

依存関係: Phase 0 → 1 → {2, 4, 5} → 3 → 6 → 7。
Phase 2 / 4 / 5 は互いに独立で並行できる。Phase 3 は 2 の完了に依存する。

### Phase 0 — 仕様凍結と RSpec の HTTP 化（4〜6 人日）

**目的**: 既存 `contest_spec.rb` を Go にも向けられる形にし、受け入れ条件を確定する。

1. **`contest_spec.rb` 465 行を HTTP 越しに書き換える。**
   - `subject { Contest.leaderboard(...) }` を
     `GET /api/audience/dashboard` などへの HTTP リクエスト + protobuf デコードに置換
   - 対象は `TARGET_BASE` 環境変数で切り替える
   - factory_bot によるデータ作成と assertion は**そのまま流用する**
   - **Rails に向けて緑になることを確認する**（ここを飛ばすと、以降の赤が
     書き換えのバグか Go のバグか区別できなくなる）
2. **`history: false` のテストを新規に足す。**
   既存の `["without history", true]` はバグで、`history: false` の経路が
   一度も実行されていない。これは `UpdateContestantDashboardJob` が使う
   本番経路なので、必ず埋める
3. `contest.rb` を読み、テストで表現しきれない部分を Markdown に補足する
   - 合成 ETag の構成要素（チーム数・最終更新・最新結果 ID）
   - `UpdateContestantDashboardJob` が Redis に焼くキーと中身
   - 追加時間（`ExtraTimeAssignment`）が `contest_open_for_team?` に与える影響
4. `buf generate` で Go バインディングを出し、ビルドが通ることを確認する
5. 非 protobuf 3 本のレスポンスを実物で採取し、golden ファイルにする

**完了条件**: HTTP 化した `contest_spec` が Rails に対して緑。
`history: false` のケースも含む。`TARGET_BASE` を差し替えるだけで Go に向けられる状態。

> この Phase をスキップすると、Phase 3 で仕様の読み違いに気付き、
> そこまでの実装をやり直すことになる。**必ず先に置く。**

### Phase 1 — 基盤（5〜8 人日）

- `cmd/portal` の起動、graceful shutdown、ヘルスチェック
- chi ルータと 107 ルートの骨組み（全部 501 を返す状態でよい）
- protobuf レンダラ（`Content-Type` の組み立て）とエラー変換 6 種
- セッション（Redis + 署名 Cookie）、5 つの値
- ガード 4 種 + 全体スイッチ（`require_staff_when_always_required` 相当）
- DB 接続、goose マイグレーション（既存スキーマを初期リビジョンとして取り込む）
- 設定読み込み（`ISUXPORTAL_*`）、slog、Sentry
- SPA シェルの `html/template`: meta タグ 8 個（CSRF 含む）+ `manifest.json` によるパック名解決

**完了条件**: `/` にアクセスすると既存の React SPA が起動し、
`/api/session` が空の `GetCurrentSessionResponse` を protobuf で返す。

### Phase 2 — 選手系 API（8〜12 人日）

| 対象 | 本数 |
|---|---:|
| `/api/session` | 1 |
| registration | 9 |
| audience（leaderboard を除く骨格） | 3 |
| contestant | 12 |
| 認証・OAuth（GitHub / Discord） | 8 |

- GitHub / Discord OAuth。**Discord のアカウント差し替えロジック**（ログイン済みで
  別アカウントの callback が来たときの挙動）を含む
- 登録の 7 状態判定（CLOSED / NOT_JOINABLE / NOT_LOGGED_IN / CREATABLE / JOINABLE / JOINED / DISQUALIFIED）
- bypass token の HMAC-SHA384 検証と 4 用途
- `Discordbot|Slackbot` の UA 弾き

**完了条件**: 既存 SPA から、登録 → ログイン → チーム作成 → 招待参加が一通り通る。

### Phase 3 — leaderboard（5〜9 人日）★リスク集中

- 可視性 3 系統 × 凍結のスコープ
- `sql.Rows` を直接回すストリーミング集計
- 非正規化カラム 3 本の維持（`MaintainBestBenchmarkResultOfTeamJob` 相当）
- 合成 ETag の生成と `If-None-Match` の処理
- `UpdateContestantDashboardJob` 相当の Redis 事前生成（チーム × 3 可視性）
- 追加時間を考慮した競技時刻判定
- `dump_leaderboard`（任意時点の確定順位）

**完了条件**: **Phase 0 で HTTP 化した `contest_spec` を `TARGET_BASE=Go` で回して全部緑**。
加えて差分テストで Rails と protobuf が一致する。

### Phase 4 — bench / envcheck API（5〜8 人日）

- bench 4 本。ジョブ払い出しの行ロック、`handle` による所有検証、
  `marked_at` の順序制御、孤児ジョブの回収
- envcheck 3 本。**JSON / text/plain / 空ボディ**の 3 形式
- `CheckerToken` と HMAC-SHA384 署名の 2 方式
- `POST /api/env_checks` の副作用（`ContestantInstance` の登録、ダミー `cloud_id`、
  捏造 `private_ipv4_address`、IP 変更時の Slack 通知）
- CloudFormation テンプレート生成（ERB → `text/template`）

**完了条件**: `supervisor/dummybench` を実際の supervisor 経由で回し、
enqueue → 払い出し → 途中報告 → 確定報告が一巡する。
既存の Go envcheck バイナリが無改修で結果を投稿できる。

### Phase 5 — admin API + 運営画面（8〜12 人日）

- admin API 26 本
- ERB 相当 3 画面（ログイン / impersonate / bypass token 発行）
- 一括コマンド実行（`golang.org/x/crypto/ssh` で `isuadmin` として接続）
- 最終検証トリガ 3 本
- 未整備チームの統計 3 本
- デバッグ用エンドポイント 4 本

**完了条件**: 既存の admin SPA から全画面が操作でき、
`/admin/last_validations` から一括実行が走って結果が収集される。

### Phase 6 — ジョブ・bot・通知（8〜12 人日）

- SQS コンシューマ（`cmd/worker`）とジョブ 20 本
- Discord bot（`cmd/bot`）とロール・ニックネーム同期
- notifyloop（`cmd/notifyloop`）
- Web Push（VAPID）。**ブラウザでの到達を実機で確認する**
- 通知の一括挿入（公開 clarification を全選手へ、バッチで）
- EC2 インスタンス同期、SSH 鍵同期、Slack 通知

**完了条件**: ベンチ完了で Web Push が実機に届き、
公開 clarification が Discord に投稿され、全選手に通知が入る。

### Phase 7 — 結合・リハーサル（5〜10 人日）

- ecspresso の taskdef を Go 版に差し替え（4 サービス）
- 差分テストの全面実行（§6）
- ダミーベンチで **900+ チーム規模**（登録上限 935）の負荷をかけ、leaderboard の生成時間とポーリング 304 経路を実測する（要件 NF-1 / NF-2 / NF-7）
- 障害シナリオの確認: ベンチのハングと孤児ジョブ回収、追加時間の付与、
  チームの失格・復帰、impersonate での問い合わせ対応
- ロールバック手順の確認（§7）

**完了条件**: リハーサルを 1 回通し、既知の不具合がゼロ。

### 工数まとめ

| Phase | 内容 | 人日 |
|---|---|---:|
| 0 | 仕様凍結 + RSpec の HTTP 化 | 4〜6 |
| 1 | 基盤 | 5〜8 |
| 2 | 選手系 API | 8〜12 |
| 3 | leaderboard | 5〜9 |
| 4 | bench / envcheck | 5〜8 |
| 5 | admin | 8〜12 |
| 6 | ジョブ・bot・通知 | 8〜12 |
| 7 | 結合・リハーサル | 5〜10 |
| | **合計** | **48〜77** |

Phase 0 は RSpec の HTTP 化で +1 人日ぶん増えたが、その分 Phase 3 が
**実行可能な受け入れ条件を持った状態で始められる**ため −1 人日。合計は変わらない。

---

## 5. スケジュール

本日 2026-08-23、競技日 2026-11-25 → **残り 94 日（約 13.4 週）**。

### 1 人体制

| 期間 | Phase |
|---|---|
| 8 月末 | 0 |
| 9 月上旬 | 1 |
| 9 月中〜下旬 | 2 |
| 10 月上旬 | 3 |
| 10 月中旬 | 4 |
| 10 月下旬 | 5 |
| 11 月上旬 | 6 |
| 11 月中旬 | 7 + カットオーバー |

**間に合うが、Phase 7 に遅延の吸収余地がない。**
どこかで 1 週間遅れると、リハーサルを削ることになる。

### 2 人体制（推奨）

| 期間 | A | B |
|---|---|---|
| 8 月末 | Phase 0（共同） | Phase 0（共同） |
| 9 月上旬 | Phase 1 | 契約テスト基盤（§6）の構築 |
| 9 月中旬 | Phase 2 | Phase 4 |
| 9 月下旬 | Phase 2 → 3 | Phase 5 |
| 10 月上旬 | Phase 3 | Phase 5 → 6 |
| 10 月中旬 | Phase 6（共同） | Phase 6（共同） |
| 10 月下旬 | Phase 7 | Phase 7 |
| 11 月 | **予備 3 週間** | |

**11 月をまるごと予備に回せる。** データ移行（`isucon2026-portal` から）や
ISUCON2026 用 AMI / CloudFormation の作成もこの期間に収まる。

3 人以上に増やしても短縮効果は小さい。Phase 0 と 3 が直列で律速になる。

---

## 6. テスト戦略

### 既存 RSpec の実態

「818 行で薄い」は不正確だった。内訳はこうなっている。

| ファイル | 行 | 中身 |
|---|---:|---|
| `spec/models/contest_spec.rb` | **465** | **`.leaderboard` のみ。実質これが全部** |
| `spec/factories/*.rb` 5 本 | 132 | テストデータ |
| `spec_helper.rb` + `rails_helper.rb` | 166 | boilerplate |
| `spec/models/*_spec.rb` 11 本 | 55 | 全部 `pending "add some examples"` の 5 行スタブ |

**実質のテストは 465 行 + factories 132 行で、全部 leaderboard に集中している。**
カバーしている context は以下。

- simple ranking / score history / errored runs / errored・failed からの resume
- 同点時は先にスコアを取ったチームが上
- **post-freeze results × {admin, anonymous, team}**
- **post-close results × {admin, anonymous, team}**
- in-progress records

**本計画で最大のリスクとした「凍結 × 可視性 3 系統」が、まさにテストされている。**
これは移植の受け入れ条件としてそのまま使える資産である。

### 既知の穴: `history: false` が未テスト

`contest_spec.rb` 29〜31 行にバグがある。

```ruby
[
  ["with history", true],
  ["without history", true],   # ← false のはず
].each do |(ctxname, history)|
```

2 つ目が `true` になっており、**`history: false` の経路が一度も実行されていない**。
そして `UpdateContestantDashboardJob` は `Contest.leaderboard(history: false, now: now)` を呼ぶ。
つまり**本番でダッシュボードを事前生成する経路が未検証**。ここは新規に書く。

### 方針: 既存 RSpec を HTTP 越しに転用する

`contest_spec.rb` は model spec で、`Contest.leaderboard` を in-process で呼び
`Time` と config を stub している。そのままでは Go に向けられない。
**HTTP 越しに書き換えれば、資産の大半をそのまま使える。**

書き換えが必要なのは `subject` の取得方法と stub の渡し方だけ。

- **factory_bot によるデータ作成はそのまま使える**（Go も同じ MySQL を見る）
- **assertion もそのまま使える**（protobuf の Ruby バインディングでデコードする）

```ruby
subject do
  res = Net::HTTP.get_response(URI("#{TARGET_BASE}/api/audience/dashboard"))
  Isuxportal::Proto::Services::Audience::DashboardResponse.decode(res.body).leaderboard
end
```

`TARGET_BASE` を環境変数にしておけば、**同じ spec を Rails にも Go にも向けられる**。

### 実施順序

段階を 1 つ挟むのが要点。

```
1. RSpec を HTTP 越しに書き換え、Rails に向けて緑にする
2. Go で実装
3. 同じ RSpec を Go に向ける（TARGET_BASE を差し替えるだけ）
4. 全部通ったら Go のテストに書き換える
```

**1 を飛ばしてはいけない。** 赤が出たときに「書き換えのバグ」と「Go のバグ」を
区別できなくなる。Rails で緑を確認しておけば、以降の赤はすべて Go 側の問題だと確定する。

4 は必須ではない。Ruby の spec を CI に残したままでも運用はできる。
Rails 本体を消したあとも `rspec` + `google-protobuf` + `factory_bot` だけは
テスト用に残す、という形が現実的。

### Go 側に必要なテスト用フック

| フック | 用途 | 実装の容易さ |
|---|---|---|
| セッション注入 | anonymous / team / admin の切り替え | ヘッダで `contestant_id` / `staff` を渡す。テスト環境でのみ有効化 |
| 現在時刻の上書き | `allow(Time).to receive(:now)` の代替 | `Contest.leaderboard(now:)` 引数と `dump_leaderboard` の `when=` が既存。同じ仕組みを流用 |
| 競技時刻設定の上書き | `contest_start` / `freeze` / `end` の stub | `ISUXPORTAL_TIMING_*` が既に環境変数。実質タダ |

### 層構成

| 層 | 内容 | いつ |
|---|---|---|
| 1 | **転用した `contest_spec`**（HTTP 越し）。leaderboard の受け入れ条件 | Phase 0 で HTTP 化、Phase 3 で Go に向ける |
| 1 | **差分テスト**。Rails と Go を同じ DB に向けて並べ、全 54 本のレスポンスを突き合わせる | Phase 1 と並行して構築、Phase 2 以降で常時 |
| 3 | **疎通テスト**。dummybench / envcheck バイナリ / Web Push の実機 | Phase 4・6 |

層 1 が leaderboard を、層 2 が残り 53 本を、層 3 が外部プロセスとの境界を担保する。

### 層 2: 差分テストの構成

```
              ┌──────────┐
  リクエスト ─┤ 比較ドライバ ├─→ Rails :3000 ─┐
              └──────────┘                  ├─→ 同一 MySQL / Redis
                    │        ─→ Go :3001 ────┘
                    ↓
            protobuf をデコードして比較
```

- 比較はデコード後に行う（フィールド順の揺れを吸収）
- タイムスタンプなど非決定な値は許容リストで除外
- テストデータは既存 factory を流用して生成

layer 1 が明示的な assertion で「意味」を守るのに対し、
layer 2 は assertion を書かずに「全エンドポイントの一致」を機械的に見る。両方要る。

---

## 7. カットオーバー

### 前提

- DB スキーマは同一なので、**DB の切り替えは発生しない**
- セッションは互換しないので、**切り替え時に全員のログインが切れる**

### 手順

1. 登録締切（**9-30**）の通過を待つ
2. ステージングで Go 版を 1 週間以上動かし、差分テストを回し続ける
3. 切り替え日を決め、**事前に「再ログインが必要」と Discord で告知する**
4. ecspresso で app / worker / bot / notifyloop の 4 サービスを Go 版に切り替え
5. Rails 版の taskdef リビジョンを**残しておく**
6. 切り替え後 30 分は差分テストを本番相当のリードオンリー要求で回す

### ロールバック

`ecspresso rollback --config app.yml` で Rails 版のタスク定義に戻す。
DB スキーマを変えていないのでデータの巻き戻しは不要。
再度ログインが切れる点だけ許容する。

**推奨時期**: 10 月中（リハーサルより前）。
競技日直前の切り替えは避ける。

---

## 8. リスクと打ち手

| # | リスク | 影響 | 打ち手 |
|---|---|---|---|
| 1 | **実戦検証を失う** | 当日に未知の不具合 | 差分テスト（§6 層 1）。Rails を消さずに残し、リハーサルまで並走比較する |
| 2 | leaderboard の仕様読み違い | **当日に順位が壊れる** | Phase 0 を独立させ、Ruby 側でテストして仕様を確認してから Go を書く |
| 3 | Phase 7 の圧縮 | リハーサル不足 | 2 人体制にして 11 月を予備に回す |
| 4 | protobuf の enum 値のずれ | 状態表示が壊れる | `ContestantInstance.status` は proto の descriptor から Rails の enum を動的生成している。移行前に DB の実値を確認する |
| 5 | Web Push が届かない | 通知機能の欠落 | Phase 6 で実機検証。最悪、アプリ内通知のみで運用する縮退を用意 |
| 6 | 非 protobuf 3 本の形式ずれ | envcheck が動かない | Phase 0 で golden ファイルを採取し、Phase 4 で照合 |
| 7 | セッション切れの周知漏れ | 選手の混乱 | 切り替えを登録締切後にし、事前告知する |
| 8 | データ移行との衝突 | 二重の変更 | `isucon2026-portal` からの移行（[`04-data-migration.md`](../survey/portal/04-data-migration.md)）は**カットオーバーとは別日に行う** |

---

## 9. この計画を採らない場合

`isucon14-portal` をそのまま運用する選択肢との比較。

| 観点 | Go rewrite | そのまま運用 |
|---|---|---|
| 新規に書くコード | 8,200〜11,600 行 | 0 |
| 工数 | 48〜77 人日 | AMI / CFn 作成 + ベンチ接続 + データ移行のみ |
| 実戦検証 | 失う（差分テストで補う） | **保たれる** |
| 運用する言語 | Go + Rust | Ruby + Rust |
| 抱える負債 | なし | **Rails 6.1.6**、`sorah/ruby` ベースイメージ |

supervisor が Rust のまま残る以上、**rewrite で減る運用言語は Ruby ひとつ**。
実質は「Rails 6.1.6 を抱えたまま当日を迎えるか」の一点に対して
48〜77 人日を払う判断になる。

どちらを選んでも、以下は共通して必要になる。

- ISUCON2026 用 AMI と CloudFormation テンプレートの作成
- ISUReal ベンチと `benchrun` の接続層
- `isucon2026-portal` からの登録データ移行
- AWS アカウントと Terraform の向け直し

---

## 10. 着手する場合の最初の 1 週間

1. `isucon14-portal` を clone し、ローカルで Rails を起動する
   （`portal/compose.yaml` がある。`app.env.template` を埋める）
2. `factory_bot` でテストデータを入れ、既存 SPA が動く状態を作る
3. `contest.rb` を読み、leaderboard 仕様書の初稿を書く
4. 仕様書に対応する RSpec を書き、現行実装で緑にする
5. `buf generate` の出力先を Go 向けに変え、ビルドを通す
6. 差分テストの比較ドライバの骨組みを作る

この 6 つが終われば、残りは分量の問題になる。

---

## 11. 実装メモ（コードを読んで確定させた仕様）

仕様書が無いためコードからしか読めない事項のうち、**調査で確定できたもの**。
Phase 0 で書き起こす仕様書の下書きとして使う。

### 11.1 `score == -1` はセンチネル ★最重要

`BenchmarkResult#update_from_pb!` の実装。

```ruby
score: pb.score == -1 ?
         (pb.finished && !pb.passed ? 0 : (raw || 0) - (deduction || 0))
       : pb.score
```

ベンチマーカーが `score: -1` を送ると、**`score_breakdown.raw - deduction` で計算する**。
`-1` 以外ならその値をそのまま採用する。加えて `finished && !passed` なら 0 に落とす
（`set_score_zero_when_failed` でも二重に落としている）。

**proto ファイルにコメントすら無い暗黙の契約。**落とすとスコアが壊れる。

### 11.2 可視性スコープの正確な定義

```ruby
scope :visible_not_frozen, ->(team) {
  where('(team_id = ? OR marked_at < ?)', team&.id, contest_freeze)
}
scope :marked_before_contest_ended, -> { where('marked_at < ?', contest_end) }
```

| visibility | 適用されるスコープ | 意味 |
|---|---|---|
| `audience` | `marked_before_contest_ended` + `visible_not_frozen(nil)` | 凍結後は誰のスコアも見えない（`team_id = NULL` は決してマッチしない） |
| `contestant` | `marked_before_contest_ended` + `visible_not_frozen(team)` | **自チームだけ凍結後も見える。他チームは凍結前まで** |
| `admin` | なし | 無制限 |

`contest_freeze` / `contest_end` が未設定なら `where('1=1')` で素通し。

### 11.3 `teams.student` の導出規則はコードにある

```ruby
def members_all_student? = members.size > 0 && members.all?(&:student?)
before_validation :generate_student_status   # student が nil のときだけ計算
```

**「メンバーが 1 人以上いて、全員が学生」なら true。**
`Team#student?` はカラムが nil のとき動的に計算する。
`04-data-migration.md` で「導出規則を決める必要がある」としたが、答えは既存実装にある。

### 11.4 予選 / 本選の分岐は実装しない（単一開催）

isucon14-portal では `ISUXPORTAL_FINAL=1` が以下すべてを切り替えていた。

```ruby
scope :promoted, -> { config.x.contest.final ? where(final_participation: true) : where('1=1') }
def promoted? = config.x.contest.final ? final_participation : true
```

**ISUCON2026 は予選を行わず単一開催なので、この分岐を Go に持ち込まない。**
消える／固定になるものは以下。

| 対象 | isucon14-portal | Go 実装 |
|---|---|---|
| `ISUXPORTAL_FINAL` | 環境変数で分岐 | **廃止** |
| `Team.promoted` scope | `final ? where(final_participation: true) : where('1=1')` | **撤廃**。`Team.active` のみで判定する |
| `Team#promoted?` | 同上 | **撤廃**。`require_promoted_contestant` は `active?` だけ見る |
| Redis キャッシュキー | `dashboard-v2:{qualify\|final}:...` | `round` セグメントを落とす（§3 参照） |
| `extra_time_assignments.final` | `where(final: config.x.contest.final)` で引く | **常に `false` で読み書き**（理由は後述） |
| CloudFormation テンプレート | `test` / `qualify` の 2 種 | **1 種に統合**。AMI 環境変数も 1 つ |
| Discord ロール | `contestant_qualify_role*` / `contestant_final_role*` の 2 系統 | **1 系統に統合** |
| `dump_leaderboard` の `when` | `qualify-end` を特別扱い | `contest-end` にリネーム |

`teams.final_participation` カラムは **DB と proto に残す**。
フロント（`AdminTeamList` / `TeamList`）が表示に使っているため。
チーム作成時に `true` を入れ、**絞り込みには使わない**。

#### `extra_time_assignments.final` を `false` 固定にする理由

このテーブルは `[team_id, final]` に unique index を持つ。
カラムを消すとスキーマ変更になり、「DB スキーマは維持する」方針（§0-2）と
差分テスト（§6 層 2）が両方壊れる。

一方 **Rails の既定は `ISUXPORTAL_FINAL=0` なので `final: false` で引く**。
Go も `false` で統一すれば、差分テスト期間中に Rails と同じ行を読み書きできる。
カラム名が実態と合わなくなるが、スキーマを触らないほうが移行コストは低い。

### 11.5 非 admin へのマスク値

`BenchmarkResult#to_pb(admin:)` と `BenchmarkJob#to_pb(admin:)` のマスク。

| フィールド | admin | 非 admin |
|---|---|---|
| `execution.stderr` | 実値 | `""` |
| `execution.exit_status` | 実値（nil なら -1） | **`-2`** |
| `execution.exit_signal` | 実値（nil なら -1） | **`-2`** |
| `execution.signaled` | 実値 | `false` |
| `instance_name` | 実値 | `""` |
| `post_validation` | 実値 | `nil` |

`-2` は「非開示」を表す埋め草。フロントは `{admin ? ... : null}` で
表示自体をガードしているので **`-2` を読んでいない**。
意味的な依存は無いが、差分テストのバイト一致のために Go も `-2` を入れる。

### 11.6 proto の `*Query` メッセージは使われていない

```ruby
def pb
  klass.decode(request.raw_post)   # ボディのみ
end
```

`ProtobufData#pb` はリクエストボディを decode するだけ。
**GET のクエリパラメータは通常の `params[:team_id]` で読んでいる。**
proto に定義された `GetRegistrationSessionQuery` / `ListBenchmarkJobsQuery` などは
**定義があるだけで実際には使われていない**。routes.rb のコメントに釣られないこと。

### 11.7 重複 enqueue 防止はアプリ層のバリデーション

```ruby
def validate_no_concurrent_jobs
  if (pending? || running?) && where(team_id:, status: %i(pending running)).where.not(id:).count > 0
    errors.add :team_id, "must have no other concurrent job"
  end
end
```

`COUNT` を見ているだけで **DB 制約ではない**。同時リクエストで抜けうる。
Go では `SELECT ... FOR UPDATE` か部分ユニークインデックスで明示的に塞ぐこと。

### 11.8 ジョブ状態遷移

```
pending --start!(instance_name)--> running --submit_result_from_pb!--> finished / errored
   |                                  |
   +--cancel!--> cancelled            +--error!--> errored
```

- `closed?` = `errored? || cancelled? || finished?`
- `handle` は `instance_name` がセットされたときだけ生成（`SecureRandom.urlsafe_base64(64)`）
- `errored` の判定は `result.errored?` = `finished && (signaled? || exit_status != 0)`
- **`error!` は自分で `BenchmarkCompletionJob` を enqueue するが、
  `submit_result_from_pb!` は呼び出し側（コントローラ）が enqueue する。** 非対称なので注意

### 11.9 `BenchmarkResult` の不変条件

| バリデーション | 内容 |
|---|---|
| `validate_immutable_after_finish` | finished 後の変更を拒否（**development 環境では無効**） |
| `validate_marked_at_linear` | `marked_at` は単調増加のみ |
| `validate_team_must_not_change` | `team_id` の変更を拒否 |
| `validate_finished_and_passed` | `finished` なら `passed` が nil でないこと |

### 11.10 その他

- **チーム上限 3 人はハードコード**。`joinable?` が `members.count < 3`
- **`benchmark_executions` はデッドテーブル**。`schema.rb` にあるだけでモデルも参照も無い。移植不要
- **`config.x` の全量は `config/environments/development.rb` の 70〜137 行にある**。
  Go の config 構造体はこれを写せばよい。`production.rb` は同じキーを
  `ENV.fetch`（必須化）にしているだけ
- **AZ は `['apne1-az1', 'apne1-az2', 'apne1-az4']` がコードに直書き**。
  チーム作成時に `team.id % zones.size` で割り当てる

---

## 12. 未解決の曖昧さ

**運営判断か、追加調査が要るもの。**着手前に潰しておきたい順に並べた。

> **解決済み**: 「予選と本選を両方やるのか」は *単一開催（本選のみ）* で確定（§11.4）。
> 「ETag の突き合わせ位置」「フロントのポーリング間隔」は実測で確定
> （要件定義 CT-5 / NF-1。ダッシュボードは事前生成キャッシュの SHA-384 を ETag に `stale?` で 304、
> ポーリングはダッシュボード 10 秒 / 通知 20 秒 / ジョブ詳細 5 秒 / 配信 2.5 秒）。
> 未確定の最新一覧は要件定義 §7（OQ-1〜11）に移した。

| # | 論点 | なぜ効くか | 潰し方 |
|---|---|---|---|
| 1 | **体制は 1 人か 2 人か** | §5 のスケジュールが変わる。1 人だとリハーサルの余裕が消える | 運営判断 |
| 2 | **`isucon2026-portal` からのデータ移行と、Rails → Go 切り替えの順序** | 両方やるなら順序が要る。同日にやるべきではない | 運営判断 |
| 3 | **`survey_response` の粒度の不一致** | proto では `BenchmarkResult` にぶら下がるが、DB は `has_one :survey_response` で **チームごとに 1 件**。最新のベンチ結果で上書きされる。意図的か不明 | コード追読 or 運営に確認 |
| 4 | **ETag の突き合わせ位置** | `Contest.leaderboard_etag` は読んだが、`If-None-Match` をどのコントローラでどう比較しているか未確認。ポーリング負荷に直結する | `api/audience/dashboards_controller.rb` を読む |
| 5 | **Redis のキー全量** | セッション / `Rails.cache`（leaderboard 30 秒）/ `dashboard-v2:*` / `leaderboard-v2:*` の 4 系統は確認済み。他に無いか未確認 | `Rails.cache` と `redis` の全参照を洗う |
| 6 | **フロントのポーリング間隔と ETag の使い方** | Go 側の負荷見積りに効く | `ApiClient.ts` を読む |
| 7 | **`instance_name` が ECS 上で何になるか** | supervisor は `%H`（ホスト名）を送る。ECS Fargate だとタスクごとに変わる。孤児ジョブ回収（`cancel_owned`）の識別子なので、再起動で変わると回収漏れが起きうる | supervisor の taskdef を確認 |
| 8 | **CloudFormation テンプレートの生成インターフェース** | ISUCON2026 用に作り直す前提だが、ERB に渡している変数（`zone_id` / `token` / `portal_credentials` / `portal_host` / `ami_id`）以外に何が要るか未確認 | `cf_templates/*.yaml.erb` 2 種を読む |
| 9 | **Discord の `support_comm_channel_roles`** | `729682389441445921=729679151396552704,...` という形式でチャンネルとロールを対応させている。ISUCON2026 の Discord 構成に合わせて作り直す必要がある | 運営の Discord 設計を確認 |
| 10 | **New Relic → Prometheus の乗り換え可否** | 既存の taskdef が New Relic 前提の環境変数を持つ可能性。ALB のヘルスチェックとは別 | `ecs/taskdef/lib/app.libsonnet` を読む |
| 11 | **`ISUXPORTAL_ADMIN_ONLY`（全体を運営限定にするスイッチ）の運用** | リハーサル時に使う想定だが、いつ落とすかの手順が文書化されていない | 運営手順として決める |

**1（体制）は §5 のスケジュールの前提を動かす。**先に決めてから着手するのが望ましい。

なお単一開催が確定したことで Phase 2 と 5 の実装量は減るが、
**工数見積り（48〜77 人日）とスケジュール（§5）は据え置く。**
削れた分は遅延の吸収余地として扱う。
