# バックエンドのみ Go rewrite の見積り

調査日: 2026-08-21。`isucon14-portal` @ `4c2b99a` の実装を実測し、
**バックエンド（Rails アプリ）だけを Go に置き換える**場合の作業量を算出した。

前提は以下。

- ポータルのホスティングは **AWS**（さくらへの移設は要件ではない）
- 選手の競技環境は **AWS**（EC2 + CloudFormation）
- **フロントエンド・supervisor・envcheck は書き直さない**

## 結論

| 項目 | 値 |
|---|---|
| 移植対象の Ruby | **4,858 行**（生成コードと dummy bench を除く） |
| 想定する Go 実装 | **8,200〜11,600 行** |
| 工数 | **48〜77 人日 ≒ 2.5〜4 人月** |
| 1 人で着手した場合 | 2.5〜4 ヶ月。競技日まで約 14 週なので**間に合うがリハーサルの余裕が薄い** |
| 2 人で着手した場合 | 1.5〜2.5 ヶ月。Phase 2 / 5 / 6 を並行できる |
| 最大のリスク | `contest.rb` 274 行（leaderboard）と、**実戦検証済みという性質を失うこと** |

---

## 1. 書き直さないもの

バックエンドのみの rewrite なので、以下はそのまま残る。これが見積りの前提。

| 資産 | 規模 | なぜ残せるか |
|---|---|---|
| React SPA | **9,957 行**（TSX/TS） | API 契約が protobuf なので、サーバの実装言語を知らない |
| protobuf 定義 | 46 ファイル 1,033 行 | `buf` で Go / TS / Ruby / Rust を一括生成できる |
| supervisor | Rust 9 ファイル | HTTP + protobuf でポータルを叩くだけ。**Rust のまま維持する方針で確定**（2026-08-23） |
| benchrun（ベンチ向けライブラリ） | Go | すでに Go。`supervisor/dummybench/benchrun/` |
| envcheck | Go | すでに Go。CloudFormation 前提も AWS なら流用可 |
| Terraform | 24 ファイル 1,634 行 | module 化済み。アカウントとドメインの差し替えのみ |
| ecspresso / jsonnet / sops | 16 ファイル | app コンテナの中身が変わるだけ |

生成済み protobuf バインディング **1,713 行**（`portal/lib/isuxportal/**/*_pb.rb`）も
`buf generate` で Go 版が出るため移植対象外。

### supervisor を Rust のまま残すコストはゼロ

`buf.gen.yaml` はすでに Rust（prost）を生成対象に含んでいる。

```yaml
plugins:
  - remote: buf.build/protocolbuffers/go        # → supervisor/dummybench/gen
  - remote: buf.build/bufbuild/es:v2.0.0        # → portal/proto     (TypeScript)
  - remote: buf.build/protocolbuffers/ruby      # → portal/lib
  - remote: buf.build/grpc/ruby
  - remote: buf.build/community/neoeinstein-prost:v0.4.0  # → supervisor/gen  (Rust)
```

`pnpm buf generate` の一発で Go と Rust が同時に出るため、
supervisor を Rust に据え置いてもツールチェーンは増えない。
Ruby 向けの 2 プラグインを外すだけで済む。

必要な編集は 1 箇所。managed mode の `go_package_prefix` が
`github.com/isucon/isucon14-portal/supervisor/dummybench/gen` を指しているので、
バックエンドが Go になるなら共有できる場所へ向け直す。

```yaml
managed:
  override:
    - file_option: go_package_prefix
      value: github.com/isucon/isucon2026/portal/gen   # 例
```

そして Go の出力先（`out:`）も `supervisor/dummybench/gen` から
バックエンドと共有するディレクトリへ移す。

## 2. 移植対象の実測

`isucon14-portal/portal/` を実測した。

| 部位 | Ruby 行数 | ファイル数 | 備考 |
|---|---:|---:|---|
| `app/controllers` | **1,614** | 48 | API 48 本 + HTML 3 画面 + 認証 |
| `app/models` | **1,232** | 23 | うち `contest.rb` が 274 |
| `app/jobs` | **622** | 20 | ActiveJob |
| `app/views`（ERB） | **241** | 10 | 実ページは 3 画面 173 行のみ |
| `config/routes.rb` | **227** | 1 | 実ルート 107 本（大半はコメント） |
| `config/initializers` | **328** | 19 | omniauth / protobuf renderer / sentry / shoryuken 等 |
| `config/environments` | **354** | 3 | Rails 固有。Go では大部分が消える |
| `bin/isuxportal-discord-bot` | **210** | 1 | Discord bot 常駐 |
| `lib/monkey_patches` | **28** | 1 | discordrb 拡張 |
| `app/helpers` | **2** | 1 | 実質空 |
| **合計** | **4,858** | | |

移植対象外: 生成 pb **1,713 行**、`bin/isuxportal-dummy-benchmarker.rb` **178 行**（任意）。

### controllers の内訳

| ディレクトリ | 行数 | ファイル |
|---|---:|---:|
| `api/admin` | 473 | 12 |
| `api/registration` | 254 | 7 |
| `api/` 直下（基底・env_checks・session） | 193 | 3 |
| `app/controllers/` 直下（application / sessions / root / broadcast_view） | 187 | 4 |
| `api/contestant` | 177 | 7 |
| `api/bench` | 115 | 3 |
| `admin`（HTML 3 画面 + debug） | 104 | 6 |
| `api/audience` | 70 | 3 |
| `api/concerns`（ProtobufData） | 21 | 1 |
| `contestant` / `registration`（SPA シェル返すだけ） | 20 | 2 |

### models の内訳

| ファイル | 行数 | 移植の性質 |
|---|---:|---|
| `contest.rb` | **274** | **最難**。leaderboard・凍結・ETag・競技時刻の判定 |
| `benchmark_job.rb` | 150 | 状態遷移機械 |
| `benchmark_result.rb` | 146 | 可視性スコープ（`of_visibility`） |
| `team.rb` | 118 | スコープと非正規化カラム 3 本 |
| `cloud_formation.rb` | 88 | ERB でテンプレート生成 |
| `contestant.rb` | 73 | |
| `bypass_token.rb` / `checker_token.rb` | 50 / 50 | HMAC 署名 + protobuf ペイロード |
| `clarification.rb` / `env_check.rb` | 45 / 45 | |
| `contestant_instance.rb` | 38 | proto の enum から動的に enum 生成 |
| `avatar.rb` | 33 | S3 presigned |
| `notification.rb` / `push_subscription.rb` | 31 / 31 | |
| `instance_command_execute_request(_result).rb` | 17 / 17 | |
| その他（coupon / survey_response / ssh_public_key / extra_time_assignment / application_record） | 26 | ほぼ宣言のみ |

### jobs の内訳

| ファイル | 行数 |
|---|---:|
| `maintain_discord_contestant_roles_job.rb` | 63 |
| `update_contestant_dashboard_job.rb` | 62 |
| `clarification_response_job.rb` | 58 |
| `benchmark_completion_job.rb` | 44 |
| `sync_ssh_keys_of_contestant_job.rb` | 44 |
| `sync_discord_member_state_of_all_contestant_job.rb` | 40 |
| `execute_on_contestant_instance_job.rb` | 33 |
| `sync_ssh_keys_of_all_contestants_job.rb` | 33 |
| `deliver_push_notification_job.rb` / `sync_contestant_instances_job.rb` / `sync_discord_member_state_of_contestant_job.rb` | 28 × 3 |
| `maintain_best_benchmark_result_of_team_job.rb` | 23 |
| `execute_on_multiple_contestant_instances_job.rb` | 22 |
| `clear_contest_data_job.rb` / `long_running_check_job.rb` / `slack_webhook_job.rb` | 21 × 3 |
| `discord_announce_job.rb` | 17 |
| `application_job.rb` / `fire_test_notification_job.rb` / `clarification_acceptance_job.rb` | 36 |

## 3. Go 実装の想定行数

Rails → Go では、ActiveRecord の宣言が手書き SQL と構造体に展開され、
暗黙の例外処理が明示的な `if err != nil` になるため **1.5〜2.5 倍**に膨らむ。
逆に `config/environments` のような Rails 固有の設定はほぼ消える。

| レイヤ | Go 想定行数 | 元の Ruby | 倍率の理由 |
|---|---:|---|---|
| ルーティング + ミドルウェア + セッション | 400〜600 | routes 227 + 一部 initializers | ガード 4 種を明示的に書く |
| protobuf レンダラ + エラー変換 | 150〜250 | concerns 21 + api/application 相当 | `Content-Type` と `Error` メッセージの生成 |
| リポジトリ層（16 テーブル） | 2,200〜3,000 | models 958（contest.rb 除く） | ActiveRecord のスコープ・バリデーション・enum が全部手書き。`sqlc` を使えばクエリは `.sql` に外出しできる |
| **leaderboard + キャッシュ + ETag** | **500〜800** | `contest.rb` 274 | 生 SQL のストリーミング、可視性 3 系統 × 凍結、合成 ETag |
| ハンドラ（API 54 + HTML） | 2,400〜3,200 | controllers 1,614 | protobuf の組み立てと 3 段ガード |
| jobs + ワーカー基盤 | 1,100〜1,650 | jobs 622 | SQS consumer か自前ワーカーの実装分を含む |
| OAuth（GitHub / Discord） | 250〜400 | omniauth initializer + sessions_controller | `golang.org/x/oauth2`。Discord のアカウント差し替えロジックを含む |
| Web Push（VAPID） | 200〜300 | job 28 + initializer | 暗号化まわり |
| Discord bot | 300 | bin 210 + monkey patch 28 | `discordgo` |
| CloudFormation テンプレート生成 | 150〜250 | `cloud_formation.rb` 88 | ERB → `text/template` |
| ERB 3 画面 + SPA シェル | 250〜350 | views 241 | `html/template` でほぼ同量 |
| 設定・起動・観測性（Sentry / metrics / graceful shutdown） | 300〜500 | environments 354 + initializers 一部 | Rails が暗黙にやっていた分を明示的に書く |
| **合計** | **8,200〜11,600** | **4,858** | 約 1.7〜2.4 倍 |

## 4. 工数

### 前提

- 1 人日あたりの新規 Go コードを **150〜250 行**とする（テスト込み、既存 Ruby から仕様を読み取りながら書く速度）
- 仕様書は存在しないため、**すべてコードから読み取る**
- 既存の RSpec 818 行のうち実質は `contest_spec.rb` 465 行で、**全部 leaderboard に集中している**（後述）

### 段階別

| Phase | 内容 | 人日 |
|---|---|---:|
| 0 | **仕様凍結**。`contest.rb` を読み解いて leaderboard の仕様書を起こす。protobuf 契約と非 protobuf 3 本の形式を固定する | 3〜5 |
| 1 | **基盤**。ルータ・セッション（Redis）・protobuf レンダラ・DB 接続・設定・ログ | 5〜8 |
| 2 | **選手系 API**。`/api/session`、registration 9 本、audience 3 本、contestant 12 本 | 8〜12 |
| 3 | **leaderboard**。可視性 3 系統・凍結・合成 ETag・Redis 事前生成・非正規化カラムの維持 | 6〜10 |
| 4 | **bench API + envcheck API**。外部プロセス（supervisor / Go チェッカー）との互換検証が重い | 5〜8 |
| 5 | **admin API 26 本 + ERB 3 画面**。impersonate / bypass token / 一括実行 | 8〜12 |
| 6 | **jobs 20 本 + ワーカー + Discord bot + Web Push** | 8〜12 |
| 7 | **結合・疎通・リハーサル**。ダミーベンチでの一巡、通知の実機確認 | 5〜10 |
| | **合計** | **48〜77** |

### カレンダー

本日 2026-08-21、競技日 2026-11-25 → **残り約 14 週（96 日）**。登録締切は 9-30。

| 体制 | 所要 | 評価 |
|---|---|---|
| 1 人 | 2.5〜4 ヶ月 | **間に合うがリハーサルの余裕が薄い**。Phase 7 が圧縮されると当日リスクが上がる |
| 2 人 | 1.5〜2.5 ヶ月 | Phase 2 / 5 / 6 を並行できる。**余裕がある** |
| 3 人以上 | 短縮効果は小さい | Phase 0 と 3 が直列で、ここが律速になる |

## 5. 守らなければならない契約

バックエンドだけ差し替えるので、外部との境界は**すべて維持する必要がある**。

| 契約 | 内容 | 落とすと起きること |
|---|---|---|
| protobuf wire format | 54 エンドポイント。`Content-Type: application/protobuf; proto=<full message name>` | フロントが読めない |
| エラーの protobuf | `isuxportal.proto.Error` に `human_message` と `human_descriptions` | エラーメッセージが表示されない |
| 非 protobuf 3 本 | `GET /api/env_check_info` は **JSON**、`GET /api/ssh_public_keys` は **text/plain**、`POST /api/env_checks` は **空ボディ** | Go の envcheck が動かない |
| envcheck の認証 2 方式 | `CheckerToken`（期限付き）と旧方式の **HMAC-SHA384 署名** | 古い AMI が繋がらない |
| SPA シェルの meta タグ 6 個 | `isux:github-auth-path` / `isux:discord-auth-path` / `isux:api-base-url` / `isux:release` / `isux:sentry-dsn` / `isux:environment` | フロントが API の場所と認証パスを知れない |
| DB スキーマ 16 テーブル | **維持すれば Rails → Go でデータ移行が不要** | 移行作業が増える |
| Redis キャッシュキー | `dashboard-v2:{round}:{visibility}[:team-{id}]` / `leaderboard-v2:{round}:admin` | Rails と並走させる場合のみ問題 |
| 変則ルート 1 本 | `GET /api/admin/instance_command_execute_requests/:request_id/:id/output` | 機械的な移植で落としやすい |
| Discordbot / Slackbot の UA 弾き | `/session/new/*` で 404 | Discord にログイン URL を貼るとセッションが壊れる |

### セッション Cookie は互換しない

Rails 6.1 + `redis-actionpack` のセッションは、暗号化 Cookie にセッション ID を入れ、
本体を **Ruby の Marshal 形式**で Redis に置く。Go から読むのは現実的でない。

したがって **Rails と Go を並走させて URL ごとに切り替える段階移行はできない**
（選手のログインが切れる）。**クリーンカットオーバー**になる。
切り替えのタイミングは登録締切（9-30）後、競技日までの間に取る。

## 6. リスク

### 最大のリスクは leaderboard ではなく「実戦検証を失うこと」

`isucon14-portal` は ISUCON14 の本番を通した実装である。書き直すとその保証が消える。
RSpec は 818 行で 2022 年から 1 行も増えていないが、**中身は偏っている**。

| ファイル | 行 | 中身 |
|---|---:|---|
| `spec/models/contest_spec.rb` | **465** | `.leaderboard` のみ。凍結 × 可視性 3 系統を含む |
| `spec/factories/*.rb` 5 本 | 132 | テストデータ |
| `spec_helper.rb` + `rails_helper.rb` | 166 | boilerplate |
| `spec/models/*_spec.rb` 11 本 | 55 | 全部 `pending "add some examples"` の 5 行スタブ |

つまり **leaderboard だけはテストがあり、それ以外は実戦でしか担保されていない**。
移植では前者を HTTP 越しに転用し、後者を差分テストで埋める。

なお `contest_spec.rb` 29〜31 行に `["without history", true]` というバグがあり、
`history: false` の経路が一度も実行されていない。
これは `UpdateContestantDashboardJob` が使う本番のダッシュボード生成経路なので、
移植時に新規で書く必要がある。

Go 版にはその担保が無い。Phase 0 で仕様書を起こし、Phase 3 で
leaderboard のテストを新規に書くことが事実上の必須条件になる。

### `contest.rb` が単体で最難

274 行のうち大半が `leaderboard` メソッド。以下が一つの塊として動いている。

- 可視性 3 系統（contestant / audience / admin）× 凍結の組み合わせ
- `teams` に非正規化された `best_benchmark_result_id` 3 本と、それを維持する `MaintainBestBenchmarkResultOfTeamJob`
- 生の MySQL ストリーミングクエリ（`cast: true, cache_rows: false, stream: true`）
- protobuf でエンコードした合成 ETag（チーム数・最終更新・最新結果 ID）
- `UpdateContestantDashboardJob` による Redis への事前生成（チーム × 3 可視性）

**救いは、この 274 行が 2022 年から 2024 年まで diff ゼロであること。**
ISUCON12 と 14 を無変更で通しており、仕様が固まっている。
Phase 0 の仕様凍結点として使える。

### その他

| リスク | 内容 | 緩和 |
|---|---|---|
| protobuf の enum 値 | `ContestantInstance.status` は proto の descriptor から Rails の enum を動的生成している。DB の整数値との対応を落とすと状態が壊れる | 移行前に DB の実値を確認する |
| Web Push の実機検証 | Go の VAPID 実装はあるが、ブラウザ側での到達確認が必要 | Phase 6 で実機試験を組む |
| ジョブ払い出しの排他 | `SELECT ... FOR UPDATE` + status 遷移。複数 supervisor が同時に叩く | RDS MySQL なら Rails と同じ挙動。TiDB を使わないなら追加検証は不要 |
| Discord API の挙動 | ニックネーム変更が 403 になるケース（サーバオーナー・上位ロール）を isucon14 は個別に処理している | 実装を写す |

## 7. rewrite しない選択との比較

| 観点 | Go rewrite | isucon14-portal をそのまま運用 |
|---|---|---|
| 新規に書くコード | **8,200〜11,600 行** | 0 |
| 工数 | 48〜77 人日 | AMI / CFn 作成 + ベンチ接続 + 依存更新のみ |
| 実戦検証 | 失う | **保たれる**（ISUCON14 本番を通した） |
| 運用する言語 | **Go + Rust** | **Ruby + Rust** |
| 依存の古さ | 解消 | **Rails 6.1.6** が残る（Ruby 3.3 / Node 20 / TS 5.5 は更新済み） |
| ベースイメージ | 自由 | `sorah/ruby:3.3-jammy`（個人メンテ）に依存 |
| AWS 依存 | どちらも同じ（前提として許容） | 同じ |

AWS 依存の解消が要件から外れ、さらに **supervisor を Rust のまま維持する方針が確定した**ため、
Go rewrite で減る運用言語は **Ruby ひとつだけ**になる。Rust はどちらの道でも残る。

**したがって Go rewrite の動機は「Ruby を運用しない」「Rails 6.1 を抱えない」の 2 点。**
実質は「Rails 6.1.6 を抱えたまま当日を迎えるか」の一点に近い。
これに 2.5〜4 人月と実戦検証の喪失を払う価値があるかどうかが判断になる。

## 8. 着手するなら

Phase 0 を独立したタスクとして先に切ることを推奨する。

1. `isucon14-portal/portal/app/models/contest.rb` を読み、
   leaderboard の可視性・凍結・ETag の仕様を Markdown に書き起こす
2. 書き起こした仕様に対して、**Ruby 側でテストを書いて現行実装に当てる**
   （Go を書く前に仕様の正しさを確認できる）
3. `buf generate` で Go バインディングを出し、protobuf 契約を固定する
4. ダミーベンチ（`supervisor/dummybench/`）で bench API の疎通経路を先に作る

この 4 つが終われば、残りは分量の問題になる。
