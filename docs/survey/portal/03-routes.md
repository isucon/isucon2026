# isucon14-portal のページとエンドポイント全量

調査日: 2026-08-21。`isucon/isucon14-portal` @ `4c2b99a` の
`config/routes.rb` / `app/views` / `app/controllers` / `app/javascript` から抽出した。

`★14` = `isucon/portal`（2022）には無く `isucon14-portal` で追加されたもの。

## 総量

| 区分 | 件数 |
|---|---:|
| ERB ファイル | 10（241 行） |
| **サーバレンダの実ページ** | **3** |
| SPA 画面 | 25 |
| protobuf API | 54 |
| 非 protobuf API | 3 |
| 総ルート数 | 107 |

## 1. ERB の全量

**ERB でページを返しているのは運営用の 3 画面だけ。**

| ファイル | 行数 | 役割 |
|---|---:|---|
| `layouts/application.html.erb` | 47 | **SPA シェル**。`<div id="app"></div>` と、設定を JS へ渡す meta タグ群 |
| `root/index.html.erb` | 1 | `<%= javascript_pack_tag 'audience' %>` の 1 行だけ |
| `registration/registration/index.html.erb` | 1 | 同上（audience バンドル） |
| `contestant/root/index.html.erb` | 1 | 同上（contestant バンドル） |
| `admin/root/index.html.erb` | 1 | 同上（admin バンドル） |
| `broadcast_view/index.html.erb` | 3 | フォント link + CSS pack + JS pack |
| `admin/sessions/new.html.erb` | 32 | **実ページ**。運営ログインフォーム |
| `admin/impersonate/index.html.erb` | 95 | **実ページ**。なりすましフォーム 3 種 |
| `admin/bypass_token/new.html.erb` | 46 | **実ページ**。トークン発行フォーム |
| `layouts/user.html.erb` | 14 | **未使用**。中身が `<p class="menu-label">Aaaaa</p>` の書きかけ |

選手・観客・配信の画面はすべて React SPA。
サーバ側は **protobuf を返す API と、SPA シェルを 1 枚返すだけの薄い層**で構成されている。

## 2. HTML を返すルート（29 本）

うち 26 は同じ SPA シェルを返してバンドルだけ切り替える。

| メソッド | パス | 種別 | バンドル / 中身 |
|---|---|---|---|
| GET | `/` | SPA | audience |
| GET | `/teams` | SPA | audience |
| GET | `/registration` | SPA | audience |
| GET | `/registration/env_check` | SPA | audience |
| GET | `/auth/failure` | SPA | audience（`root#index` に流す）★14 |
| GET | `/contestant` | SPA | contestant |
| GET | `/contestant/benchmark_jobs` | SPA | contestant |
| GET | `/contestant/benchmark_jobs/:id` | SPA | contestant |
| GET | `/contestant/clarifications` | SPA | contestant |
| GET | `/contestant/contestant_instances` | SPA | contestant |
| GET | `/contestant/discord` | SPA | contestant |
| GET | `/admin` | SPA | admin |
| GET | `/admin/teams` | SPA | admin |
| GET | `/admin/teams/:id` | SPA | admin |
| GET | `/admin/teams/:id/edit` | SPA | admin |
| GET | `/admin/benchmark_jobs` | SPA | admin |
| GET | `/admin/benchmark_jobs/:id` | SPA | admin |
| GET | `/admin/clarifications` | SPA | admin |
| GET | `/admin/clarifications/:id` | SPA | admin |
| GET | `/admin/last_validations` | SPA | admin ★14 |
| GET | `/admin/last_validations/command_result/:id` | SPA | admin ★14 |
| GET | `/admin/contestant_instances` | SPA | admin |
| GET | `/admin/unprepared_stats` | SPA | admin ★14 |
| GET | `/broadcast_view/leaderboard` | SPA | broadcast_view（専用 CSS あり） |
| GET | `/broadcast_view/score_changes` | SPA | broadcast_view |
| GET | `/broadcast_view/clock` | SPA | broadcast_view |
| GET | `/admin/session/new` | **ERB** | 運営ログイン画面。未ログインで `/admin/*` に来ると `back_to` 付きでここへリダイレクト |
| GET | `/admin/impersonate` | **ERB** | なりすまし画面。Contestant / GitHub / Discord の 3 セクション |
| GET | `/admin/bypass_token` | **ERB** | bypass token 発行画面。発行結果の URL をその場に表示 |

## 3. SPA が担う画面（25 画面）

「必要なページ」の実体。コンポーネント名が実装の単位になる。

### audience バンドル（5 画面）

| クライアントルート | コンポーネント | 内容 |
|---|---|---|
| `/` | `AudienceDashboard` | 順位表 + スコアグラフ。凍結中は観客向けの値 |
| `/teams` | `TeamList` | 参加チーム一覧 |
| `/registration` | `Registration` → `RegistrationForm` / `RegistrationStatus` / `RegistrationLogin` | 登録の 7 状態（CLOSED / NOT_JOINABLE / NOT_LOGGED_IN / CREATABLE / JOINABLE / JOINED / DISQUALIFIED）で分岐 |
| `/registration/env_check` | `EnvCheck` | 環境チェックのテンプレート配布と進捗表示 |
| `/auth/failure` | `AuthError` ★14 | OAuth 失敗時。これが無いと白画面になる |

### contestant バンドル（6 画面 + 常駐 4）

| クライアントルート | コンポーネント | 内容 |
|---|---|---|
| `/contestant` | `ContestantDashboard` | 自チームのスコア・順位・履歴グラフ |
| `/contestant/benchmark_jobs` | `ContestantBenchmarkJobList` + `ContestantBenchmarkJobForm` | 一覧と enqueue フォーム |
| `/contestant/benchmark_jobs/:id` | `ContestantBenchmarkJobDetail` | stdout / stderr / reason / スコア内訳 |
| `/contestant/clarifications` | `ContestantClarificationList` | 質問の投稿と公開済み Q&A |
| `/contestant/contestant_instances` | `ContestantContestantInstanceList` | 自チームのサーバ 3 台の IP と状態 |
| `/contestant/discord` | `ContestantDiscordPage` | Discord 連携の案内 |
| （全画面共通） | `ContestantNavbar` / `ContestClock` / `ContestantNotificationsObserver` / `ContestantNotificationSubscriptionPanel` / `ContestantCloudFormationMessage` | 通知ポーリング、Web Push 購読 UI、競技残り時間、環境構築バナー |

### admin バンドル（11 画面 + 常駐 3）

| クライアントルート | コンポーネント | 内容 |
|---|---|---|
| `/admin` | `AdminDashboard` | 運営向け順位表 + 未回答 clar の件数と最古時刻 |
| `/admin/teams` | `AdminTeamList` | 全チーム一覧 |
| `/admin/teams/:id/*` | `AdminTeamDetail` / `AdminTeamEdit` / `AdminTeamTagList` / `AdminTeamCloudFormationDownloadButton` | 詳細・編集（失格 / 隠し / 本選出場 / 学生）、CFn ダウンロード |
| `/admin/benchmark_jobs` | `AdminBenchmarkJobList` + `AdminBenchmarkJobForm` | 絞り込みとページング、代理 enqueue |
| `/admin/benchmark_jobs/:id` | `AdminBenchmarkJobDetail` + `EnqueuedBy` ★14 | 実行者の表示を含む |
| `/admin/clarifications` | `AdminClarificationList` | 未回答のみ絞り込み |
| `/admin/clarifications/:id` | `AdminClarificationDetail` | 回答・公開設定・質問文の書き換え |
| `/admin/last_validations` | `AdminLastValidations` ★14 | envcheck 一括再実行 / 一括再起動 / ベンチ一括実行のトリガ |
| `/admin/last_validations/command_result/:id` | `AdminLastValidationsCommandResult` ★14 | コマンド実行結果の一覧と個別出力 |
| `/admin/contestant_instances` | `AdminContestantInstanceList` | 全チームのインスタンス一覧 |
| `/admin/unprepared_stats` | `AdminUnpreparedStats` ★14 | SSH 鍵未登録 / Discord 未参加 / envcheck 未完了 |

### broadcast_view バンドル（3 画面）

| クライアントルート | コンポーネント |
|---|---|
| `/broadcast_view/leaderboard` | `BroadcastLeaderboard` |
| `/broadcast_view/score_changes` | `BroadcastScoreChanges` |
| `/broadcast_view/clock` | `BroadcastClock` |

## 4. 認証・セッション（8 本）

すべて HTML リダイレクトで完結する。protobuf は使わない。
omniauth ミドルウェアが `/auth/github` と `/auth/discord` を握っている。

| メソッド | パス | 挙動 |
|---|---|---|
| GET | `/session/new/github` | `back_to` を session に保存して `/auth/github` へ。**Discordbot / Slackbot の UA には 404** |
| GET | `/session/new/discord` | 同上（Discord 版） |
| GET | `/auth/github` | omniauth ミドルウェア |
| GET | `/auth/discord` | omniauth ミドルウェア。scope は `identify` |
| GET | `/auth/github/callback` | `session[:github_login]` に `{id, login, token, avatar_url}`。既存 Contestant があれば `contestant_id` も立て、SSH 鍵と Discord 状態の同期ジョブを投入 |
| GET | `/auth/discord/callback` | discriminator が `0` なら tag は username のみ。**ログイン済みなら Discord アカウントの差し替え**として扱い、Slack 通知とロール付け替え |
| DELETE | `/session` | ログアウト。`contestant_id` / `staff` / `github_login` / `discord_login` を消す |
| GET | `/session/bypass/:token` | HMAC-SHA384 署名を検証して `session[:bypass_token]` に載せる。不正なら `404 no` |

## 5. protobuf API

すべて `Content-Type: application/protobuf; proto=<full message name>`。
エラーも `isuxportal.proto.Error` を protobuf で返す（404 / 400 / 403 / 422）。

### common / registration（9 本）

| メソッド | パス | メッセージ | 認証・備考 |
|---|---|---|---|
| GET | `/api/session` | `GetCurrentSessionResponse` | 誰でも。チーム・選手・競技時刻・インスタンス・VAPID 公開鍵を一括 |
| GET | `/api/registration/session` | `GetRegistrationSessionResponse` | query: `team_id` / `invite_token` / `bypass_token`。7 状態を判定 |
| PUT | `/api/registration` | `UpdateRegistrationRequest` | 競技開始後と更新締切後は 403。チーム名とメールは代表者のみ |
| DELETE | `/api/registration` | `DeleteRegistrationRequest` | 代表者なら `withdrawn` を立てて全員削除、メンバーなら自分だけ |
| POST | `/api/registration/team` | `CreateTeamRequest` | GitHub と Discord 両方のログインが必須。AZ を `id % zones.size` で自動割当 |
| POST | `/api/registration/contestant` | `JoinTeamRequest` | 招待トークンで参加 |
| GET | `/api/registration/env_check` | `GetEnvCheckInformationResponse` | CFn テンプレート本文と進捗状態、インスタンス IP |
| PUT | `/api/registration/coupon` | `ActivateCouponRequest` | クラウドクーポンの有効化 |
| GET | `/api/registration/avatar/url` | `GetAvatarUrlResponse` ★14 | S3 の presigned PUT URL（600 秒）と公開 URL |

### audience（3 本）

| メソッド | パス | メッセージ | 備考 |
|---|---|---|---|
| GET | `/api/audience/dashboard` | `DashboardResponse` | 誰でも。ETag と Redis 事前生成キャッシュが効く |
| GET | `/api/audience/dashboard/teams/:id` | `SoloDashboardResponse` | 単一チームの履歴つき |
| GET | `/api/audience/teams` | `ListTeamsResponse` | チーム一覧 |

### contestant（12 本）

3 段のガードが全部にかかる。
`require_contestant` → `require_promoted_contestant` → `require_contest_started`（追加時間も考慮）。
競技時間外は 404。

| メソッド | パス | メッセージ | 備考 |
|---|---|---|---|
| GET | `/api/contestant/dashboard` | `DashboardResponse` | 自チームの LeaderboardItem |
| GET | `/api/contestant/benchmark_jobs` | `ListBenchmarkJobsResponse` | query: `limit` |
| POST | `/api/contestant/benchmark_jobs` | `EnqueueBenchmarkJobRequest` | `target_id`（ContestantInstance の id） |
| GET | `/api/contestant/benchmark_jobs/:id` | `GetBenchmarkJobResponse` | target / result / execution を含む |
| GET | `/api/contestant/clarifications` | `ListClarificationsResponse` | 自チームのものと公開済み |
| POST | `/api/contestant/clarifications` | `RequestClarificationRequest` | 質問の投稿 |
| GET | `/api/contestant/notifications` | `ListNotificationsResponse` | query: `after`。未指定ならサーバ側の `read` を使う |
| POST | `/api/contestant/notifications` | — | テスト通知の発火 |
| DELETE | `/api/contestant/notifications/:id` | — | 既読化 |
| POST | `/api/contestant/push_subscriptions` | `SubscribeNotificationRequest` | `endpoint` / `p256dh` / `auth` |
| DELETE | `/api/contestant/push_subscriptions` | `UnsubscribeNotificationRequest` | 購読解除 |
| GET | `/api/contestant/cloud_formation` | `GetCloudFormationResponse` | 競技用テンプレート本文 |

### admin（26 本）

全て `require_staff`（`session[:staff]`）。未ログインなら ERB のログイン画面へリダイレクト。

| メソッド | パス | メッセージ | 備考 |
|---|---|---|---|
| GET | `/api/admin/dashboard` | `DashboardResponse` | 未回答 clar の件数と最古時刻を同梱 |
| GET | `/api/admin/dashboard/teams/:id` | `SoloDashboardResponse` | |
| GET | `/api/admin/teams` | `ListTeamsResponse` | |
| GET | `/api/admin/teams/:id` | `GetTeamResponse` | detail / members つき |
| PUT | `/api/admin/teams/:id` | `UpdateTeamRequest` | Team と、指定した Contestant のみを更新 |
| GET | `/api/admin/teams/:team_id/cloud_formation` | `GetCloudFormationResponse` | query: `type` = `test` \| `qualify` |
| GET | `/api/admin/teams/:team_id/env_checks` | `ListEnvChecksResponse` | `admin_message` と生データも見える |
| GET | `/api/admin/benchmark_jobs` | `ListBenchmarkJobsResponse` | query: `team_id` / `status` / `page` / `failed_only` |
| POST | `/api/admin/benchmark_jobs` | `EnqueueBenchmarkJobRequest` | 代理 enqueue。`enqueued_by_id` に実行者を記録 ★14 |
| GET | `/api/admin/benchmark_jobs/:id` | `GetBenchmarkJobResponse` | |
| DELETE | `/api/admin/benchmark_jobs/:id` | `CancelBenchmarkJobResponse` | キャンセル |
| GET | `/api/admin/clarifications` | `ListClarificationsResponse` | query: `team_id` / `unanswered_only` |
| POST | `/api/admin/clarifications` | `CreateClarificationRequest` | 運営発信のアナウンス |
| GET | `/api/admin/clarifications/:id` | `GetClarificationResponse` | |
| PUT | `/api/admin/clarifications/:id` | `RespondClarificationRequest` | `disclose` / `answer` / 質問文の上書き |
| GET | `/api/admin/instance_command_execute_requests` | `List…Response` ★14 | 一括実行の履歴（対象数 / 完了数） |
| GET | `/api/admin/instance_command_execute_requests/:id` | `Get…Response` ★14 | チーム別の exit code と完了時刻 |
| GET | `/api/admin/instance_command_execute_requests/:request_id/:id/output` | `Get…OutputResponse` ★14 | 個別の標準出力。**パスに id が 2 つ並ぶ変則ルート** |
| POST | `/api/admin/last_validations/env_check` | `TriggerEnvCheckRequest` ★14 | `team_ids` 空なら全チーム。SSH で `systemctl restart envcheck --no-block` |
| POST | `/api/admin/last_validations/instance_restart` | `TriggerInstanceRestartRequest` ★14 | SSH で `sudo reboot` |
| POST | `/api/admin/last_validations/benchmarks` | `TriggerBenchmarksRequest` ★14 | 直近ジョブの target を引き継いで再 enqueue。`post_validation` 付き |
| GET | `/api/admin/contestant_instances` | `ListContestantInstancesResponse` | query: `team_id` |
| GET | `/api/admin/dump_leaderboard` | `GetLeaderboardDumpResponse` | query: `when` = ISO8601 か `qualify-end` |
| GET | `/api/admin/unprepared_stats/ssh_key_stats` | `GetSSHKeyStatsResponse` ★14 | SSH 鍵未登録のメンバー |
| GET | `/api/admin/unprepared_stats/discord_stats` | `GetDiscordStatsResponse` ★14 | Discord 未参加のメンバー |
| GET | `/api/admin/unprepared_stats/env_check_stats` | `GetEnvCheckStatsResponse` ★14 | envcheck 未完了のチーム |

### bench（4 本）

supervisor（Rust）が叩く。共有トークン認証。

| メソッド | パス | メッセージ | 備考 |
|---|---|---|---|
| POST | `/api/bench/receive` | `ReceiveBenchmarkJobRequest` | トークンを `secure_compare`。`team_id > 0` なら専用ベンチ。行ロックで取り合い、`job_handle` を返す |
| POST | `/api/bench/cancel_owned` | `CancelOwnedBenchmarkJobRequest` | ベンチ再起動時に自分の running を落とす。`instance_name` で識別 |
| POST | `/api/bench/benchmark_results` | `ReportBenchmarkResultRequest` | 途中経過。`job_id` + `handle` で所有検証。`marked_at` が新しいものだけ採用 |
| POST | `/api/bench/benchmark_results/completion` | `CompleteBenchmarkJobRequest` | 確定報告。`finished` と `execution` が必須 |

## 6. 非 protobuf API（3 本）

Go の `isucon-env-checker` が叩く。**形式を変えられない。**

| メソッド | パス | 形式 | 内容 |
|---|---|---|---|
| GET | `/api/env_check_info` | **JSON** | `{ami_id, ami_ids, az_id}`。query: `name` = `test-boot` \| `test-ssh` \| `contest`。未知の name は 400 |
| GET | `/api/ssh_public_keys` | **text/plain** | 1 行 1 鍵。`<public_key> <contestant_id>@<github_login>` |
| POST | `/api/env_checks` | **空ボディ** | チェック結果の投稿。`render` を呼んでいない |

### 認証が 2 方式ある

`require_valid_checker_token` を通る。`token` があれば `CheckerToken`
（期限付き、CFn テンプレートに base64 で埋め込む）を検証する。
`token` が無く `team_id` があれば、旧方式の **HMAC-SHA384 署名**で検証する。
片方だけ実装すると古い AMI が動かなくなる。

### envcheck はインスタンス登録の第 2 経路になっている

`POST /api/env_checks` で `name` が `contest<N>` の形で来ると、副作用として
`ContestantInstance` を `find_or_initialize_by(team_id, number: N)` し、
状態を RUNNING にして public IP を書き込む。このとき

- `cloud_id` は `"contest-<team_id>-<N>"` というダミー
- `private_ipv4_address` は `"192.168.0.1<N>"` という**捏造値**

つまり EC2 API を叩く `SyncContestantInstancesJob` とは**独立した経路**で
選手のサーバがポータルに登録される。IP が変わったときは Slack に通知が飛ぶ。
「インスタンス状態の自動同期」を評価する際、この 2 経路を混同しないこと。

## 7. 運営フォームの POST（5 本）

ERB の 3 画面が投げる先。protobuf ではなく通常のフォーム POST。

| メソッド | パス | 内容 |
|---|---|---|
| POST | `/admin/session` | 運営ログイン。`ISUXPORTAL_ADMIN_LOGIN` / `ISUXPORTAL_ADMIN_PASSWORD` と照合して `session[:staff]` を立てる |
| POST | `/admin/impersonate/contestant` | `session[:contestant_id]` を任意の値にする（`clear` で解除） |
| POST | `/admin/impersonate/github` | `session[:github_login]` を `{id, login}` で捏造。**登録フローのテストに使う** |
| POST | `/admin/impersonate/discord` | 同上（Discord 版） |
| POST | `/admin/bypass_token` | `expires_in` と `usages[]`（`CREATE_TEAM` / `JOIN_TEAM` / `HIDDEN_TEAM` / `LEAVE_TEAM`）からトークンを発行 |

## 8. その他（8 本）

| メソッド | パス | 形式 | 内容 |
|---|---|---|---|
| GET | `/terms` | 302 | 参加規約の外部 URL へ |
| GET | `/rules` | 302 | レギュレーションの外部 URL へ |
| GET | `/docs` | 302 | 当日マニュアルへ。**`Contest.contest_running?` が真の間だけルーティングが有効**（constraints で制御） |
| GET | `/site/sha` | text/plain | `REVISION` ファイルの内容（revision_plate） |
| GET | `/admin/slacktown` | text/plain | Slack 疎通テスト |
| GET | `/admin/sync_all_ssh_key` | text/plain | 全選手の SSH 鍵同期を投入 |
| GET | `/admin/sync_all_discord_stats` | text/plain | 全選手の Discord 状態同期を投入 |
| GET | `/admin/long_running_check` | text/plain | 滞留監視の手動実行。**これだけ `require_staff` を外している**（notifyloop サービスが外から叩く） |

## 9. 移植時に落としやすい点

1. **非 protobuf のエンドポイントは 3 本だけ**。いずれも Go の envcheck が叩く口
   （JSON / text/plain / 空ボディ）。見落とすとチェッカーが動かない形で移植が完了する
2. **SPA シェルの meta タグが契約**。`isux:github-auth-path` / `isux:discord-auth-path` /
   `isux:api-base-url` / `isux:release` / `isux:sentry-dsn` / `isux:environment` の 6 つ。
   認証パスには現在の URL が `back_to` として埋め込まれる
3. **セッションに 5 つの独立した値が載る**。`contestant_id` / `staff` / `bypass_token` /
   `github_login` / `discord_login`。後者 2 つは「ログインはしたがチームがまだ無い」状態を表現する
4. **ガードは 4 種類 + 全体スイッチ**。`require_staff` / `require_contestant` /
   `require_promoted_contestant` / `require_contest_started`。
   加えて `require_staff_when_always_required` が全ルートの手前にあり、
   設定でポータル全体を運営限定にできる（リハーサル用）
5. **変則ルートが 1 本**。`GET /api/admin/instance_command_execute_requests/:request_id/:id/output`
6. **Discordbot / Slackbot の UA を 404 で弾いている**。`/session/new/*` で。
   Discord にログイン URL を貼ったときのプレビュー取得でセッションが壊れるのを防ぐため
7. **CSRF は bench と envcheck 系で無効化されている**。認証は共有トークンと署名で行う
8. **エラーも protobuf**。`human_message`（1 行）と `human_descriptions`（複数行）。
   バリデーションエラーは後者に全件並べる
9. **`layouts/user.html.erb` は移植しない**。書きかけで参照されていない
