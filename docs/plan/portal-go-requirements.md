# ポータルバックエンド Go rewrite 要件定義

作成日: 2026-08-23。実装計画は [`portal-go-rewrite.md`](portal-go-rewrite.md)、
調査は [`../survey/portal/`](../survey/portal/README.md)。

本書は「Go 実装が満たすべきこと」を ID 付きで列挙する。
仕様の典拠はすべて `isucon14-portal` @ `4c2b99a` の実装コード。
計画書と食い違う場合は**本書が優先**（§8 に計画書への訂正を列挙した）。

ID 規約: FR = 機能要件 / CT = 互換契約 / DM = データ要件 / NF = 非機能要件 / OQ = 未確定。

## 0. 前提（確定済み）

| # | 前提 |
|---|---|
| P-1 | ホスティング・競技環境ともに AWS。1 チーム 3 台（EC2 + CloudFormation） |
| P-2 | supervisor は Rust のまま。フロントエンド（React SPA）・envcheck（Go）も無改修 |
| P-3 | **単一開催（本選のみ）**。予選分岐 `ISUXPORTAL_FINAL` は実装しない |
| P-4 | 競技日 2026-11-25、登録締切 2026-09-30。切り替えは締切後のクリーンカットオーバー |
| P-5 | `isucon2026-portal` からの登録データ移行あり（切り替えとは別日） |

## 1. アクターとスコープ

| アクター | 接点 |
|---|---|
| 選手（contestant） | SPA + protobuf API。競技時間中のみ（追加時間を含む） |
| 観客（audience） | SPA + protobuf API。認証不要 |
| 運営（staff） | SPA + protobuf API + ERB 相当 3 画面。`session[:staff]` |
| supervisor（Rust） | `/api/bench/*` 4 本。共有トークン認証 |
| isucon-env-checker（Go） | 非 protobuf 3 本。CheckerToken / HMAC 署名 |
| ブラウザ SW | Web Push（VAPID）+ Service Worker |
| 外部サービス | GitHub OAuth / Discord OAuth・Bot / Slack Webhook / EC2 / S3 / SQS |

### スコープ外（実装しない）

- 予選 / 本選の分岐一式（P-3。詳細は計画書 §11.4）
- `isucon2026-portal` 固有機能: 段階募集（締切後に切り替えるため不要）、
  登録クーポン `RegisterCoupon`、チケットのスレッド形式（clarification に集約）
- `benchmark_executions` テーブル（デッドコード）
- `layouts/user.html.erb`（未使用の書きかけ）
- 運営ログイン画面の YouTube / GIPHY 埋め込み（機能に寄与しない。任意）

## 2. 機能要件

### 2.1 認証・セッション（FR-AUTH）

| ID | 要件 |
|---|---|
| FR-AUTH-1 | GitHub OAuth。callback で `session[:github_login] = {id, login, token, avatar_url}` を保存。既存 Contestant（`github_id` 一致）がいれば `contestant_id` を立て、`github_login` カラムを最新化し、SSH 鍵同期と Discord 状態同期のジョブを投入する |
| FR-AUTH-2 | Discord OAuth（scope: `identify`）。discriminator が `'0'` なら tag は username のみ、それ以外は `username#discriminator` |
| FR-AUTH-3 | Discord callback は 3 分岐: (a) `discord_id` 一致の Contestant がいれば**そのアカウントに切り替え**、(b) ログイン済みなら **Discord アカウントの差し替え**として Slack 通知 + 旧 ID のロール剥奪 + 新 ID で同期、(c) いずれでもなければ登録フローへ |
| FR-AUTH-4 | `/session/new/{github,discord}` は `back_to`（相対パスのみ許可）を保存してから provider へ。**UA が `Discordbot|Slackbot` なら 404**（`beepbeep` パラメータで回避可） |
| FR-AUTH-5 | OAuth 失敗は `/auth/failure` へ（SPA が `AuthError` を表示） |
| FR-AUTH-6 | ログアウトで `contestant_id` / `staff` / `github_login` / `discord_login` を消す |
| FR-AUTH-7 | bypass token: `base64url(HMAC-SHA384 署名):base64url(BypassTokenPayload)` を `/session/bypass/:token` で検証し session に載せる。期限切れ・改竄は 404。用途 4 種（CREATE_TEAM / JOIN_TEAM / HIDDEN_TEAM / LEAVE_TEAM） |
| FR-AUTH-8 | 運営ログイン: `ISUXPORTAL_ADMIN_LOGIN` / `PASSWORD` と定数時間比較し `session[:staff]` を立てる。未ログインの `/admin/*` は `back_to` 付きでログイン画面へ |
| FR-AUTH-9 | impersonate 3 種（contestant / github / discord）。session の該当値を任意に設定・解除できる |
| FR-AUTH-10 | `ISUXPORTAL_ADMIN_ONLY=1` でポータル全体を staff 限定にできる（リハーサル用全体スイッチ） |

### 2.2 参加登録（FR-REG）

| ID | 要件 |
|---|---|
| FR-REG-1 | 登録セッションの 7 状態判定: CLOSED / NOT_JOINABLE / NOT_LOGGED_IN / CREATABLE / JOINABLE / JOINED / DISQUALIFIED。判定順序は `sessions_controller#show` に一致させる |
| FR-REG-2 | チーム作成は **GitHub と Discord の両ログインが揃っていること**が条件。登録期間内かつ上限未満（または bypass CREATE_TEAM） |
| FR-REG-3 | AZ は作成時に `team.id % 3` で `['apne1-az1','apne1-az2','apne1-az4']` から割当 |
| FR-REG-4 | 招待 URL（`team_id` + `invite_token`）での参加。チーム上限 **3 人**。招待締切後は bypass JOIN_TEAM が必要 |
| FR-REG-5 | 登録内容の更新は競技開始後と更新締切後は 403。チーム名・メールは代表者のみ |
| FR-REG-6 | 辞退: 代表者なら `withdrawn=true` + 全員 destroy + Discord ロール剥奪 + Slack 通知。メンバーは自分のみ（開始後は bypass LEAVE_TEAM） |
| FR-REG-7 | アイコン: S3 presigned PUT（有効 600 秒）の発行と公開 URL の返却 |
| FR-REG-8 | クーポンの有効化（`ActivateCoupon`） |
| FR-REG-9 | 新規チーム作成・辞退を Slack に通知する |

### 2.3 選手向け競技機能（FR-C）

3 段ガード必須: セッションあり → チームが active → 競技時間内（**チーム別追加時間を考慮**。`ExtraTimeAssignment` を `final=false` で引く）。時間外は 404。

| ID | 要件 |
|---|---|
| FR-C-1 | ダッシュボード: 自チームの `LeaderboardItem`（履歴つき）。凍結中も自チームの結果は見える |
| FR-C-2 | ベンチ enqueue: `target_id`（自チームの ContestantInstance）を指定。**同時 pending/running は 1 件まで** |
| FR-C-3 | ジョブ一覧（`limit`）・詳細。詳細には target / result / execution を含む。マスク規則は CT-6 |
| FR-C-4 | clarification の投稿と閲覧（自チーム分 + 公開分） |
| FR-C-5 | 通知一覧（`after` 優先、無ければサーバ側 `read`）・既読化・テスト通知の発火 |
| FR-C-6 | Web Push 購読の登録（endpoint / p256dh / auth）・解除 |
| FR-C-7 | 競技用 CloudFormation テンプレートの取得 |

### 2.4 観客向け（FR-A）

| ID | 要件 |
|---|---|
| FR-A-1 | leaderboard（認証不要）。凍結後の結果は誰の分も含まない。競技終了後の結果も含まない |
| FR-A-2 | チーム個別ダッシュボード（履歴つき） |
| FR-A-3 | チーム一覧（名前・メンバー名・学生・hidden 除外） |
| FR-A-4 | 配信 3 画面（leaderboard / score_changes / clock）が同じ API で動くこと |

### 2.5 運営向け（FR-S）

| ID | 要件 |
|---|---|
| FR-S-1 | 運営 leaderboard（無制限可視）+ 未回答 clar の件数・最古時刻 |
| FR-S-2 | チーム一覧・詳細・更新（失格 / 隠し / 学生、指定 Contestant のみ部分更新） |
| FR-S-3 | ジョブ一覧（team_id / status / failed_only / page）・詳細（enqueued_by 表示）・キャンセル・代理 enqueue（実行者を `enqueued_by_id` に記録） |
| FR-S-4 | clarification の回答・公開・質問文上書き（`original_question` 保存）・運営発信作成 |
| FR-S-5 | チーム別 CFn テンプレート取得・env_check 結果閲覧（admin_message / raw_data 含む） |
| FR-S-6 | インスタンスへの SSH コマンド一括実行（`isuadmin`、秘密鍵はファイルパス指定）と結果収集（exit code / 出力 / 完了時刻） |
| FR-S-7 | 最終検証トリガ 3 種: envcheck 再実行（`systemctl restart envcheck --no-block`）/ 一括 reboot / ベンチ一括 enqueue（直近ジョブの target 引き継ぎ、`post_validation` 付き） |
| FR-S-8 | 未整備統計 3 種（SSH 鍵未登録 / Discord 未参加 / envcheck 未完了） |
| FR-S-9 | `dump_leaderboard`（`when` = ISO8601 または `contest-end`）で任意時点の確定順位 |
| FR-S-10 | bypass token 発行画面（期限 + 用途チェックボックス → URL 表示） |
| FR-S-11 | 追加時間の付与が反映されること（付与 UI は Rails 同様に無くてよい。DB 直接投入を許容） |
| FR-S-12 | デバッグ: Slack 疎通 / SSH 鍵全同期 / Discord 全同期 / 滞留チェック手動実行（**最後のみ認証なし** — notifyloop が叩く） |
| FR-S-13 | 競技データの全削除（リハーサル後のリセット） |

### 2.6 ベンチマーカー連携（FR-B）

| ID | 要件 |
|---|---|
| FR-B-1 | `receive`: 共有トークンを定数時間比較。pending ジョブを**行ロックで排他取得**し running へ遷移、`job_handle`（job_id / handle / target private IP / 全 3 台の public IP）を返す。無ければ `job_handle` なし。`team_id > 0` なら専用ベンチとして絞る |
| FR-B-2 | `cancel_owned`: 同一 `instance_name` の running を errored に落とす（ベンチ再起動時の孤児回収） |
| FR-B-3 | 途中報告: `job_id` + `handle` で所有検証。`marked_at` 必須。**既存より新しい `marked_at` のみ採用**。`execution` を含んではならない（400） |
| FR-B-4 | 確定報告: `finished` と `execution` が必須。closed に遷移したら `BenchmarkCompletionJob` を投入 |
| FR-B-5 | `survey_response`（実装言語）が添付されていれば保存 |
| FR-B-6 | ISUReal ベンチ本体は `benchrun.Reporter` で `BenchmarkResult` を supervisor に渡す接続層を実装する（別リポジトリ作業。CT-7 のセンチネル規則に従う） |

### 2.7 envcheck 連携（FR-E）

| ID | 要件 |
|---|---|
| FR-E-1 | `GET /api/env_check_info` → **JSON** `{ami_id, ami_ids, az_id}`。`name` = test-boot / test-ssh / contest。未知は 400 |
| FR-E-2 | `GET /api/ssh_public_keys` → **text/plain**。`<key> <contestant_id>@<github_login>` を 1 行ずつ、末尾改行 |
| FR-E-3 | `POST /api/env_checks` → **空ボディ**で結果保存 |
| FR-E-4 | 認証 2 方式: CheckerToken（期限付き）、無ければ `team_id` + HMAC-SHA384 `signature`（旧 AMI 互換） |
| FR-E-5 | `name=contest<N>` の副作用: `ContestantInstance` を (team_id, number=N) で upsert、RUNNING、public IP 記録、`cloud_id="contest-<team>-<N>"`、`private_ipv4_address="192.168.0.1<N>"`。**競技終了後は更新しない**。IP 変化と失敗（5 分デバウンス）は Slack 通知 |

### 2.8 ジョブ・外部連携（FR-J）

**cron 基盤は不要。**全ジョブはイベント起点か常駐ループ。トリガの全量:

| ジョブ | トリガ |
|---|---|
| BenchmarkCompletion（best 更新 → ダッシュボード再生成 → Push → Slack） | ベンチ確定 / error! |
| MaintainBestBenchmarkResultOfTeam | 上記から。3 可視性ぶんの best を更新 |
| UpdateContestantDashboard | 上記から（`team:` 指定で該当チーム + 全体のみ再生成） |
| ClarificationResponse（Discord アナウンス + 全選手通知一括挿入 + 当該チーム Push） | 運営の回答・公開 |
| DeliverPushNotification（410/404 で購読自動削除） | 通知作成 |
| SyncSshKeysOfContestant（`github.com/<login>.keys`、ユーザー token 利用） | GitHub ログイン / 運営一括 |
| SyncDiscordMemberStateOfContestant / MaintainDiscordContestantRoles | ログイン / 差し替え / 辞退 / 運営一括 |
| SyncContestantInstances（EC2 タグ `IsuconTeamID` / `IsuconInstanceNum` で同期） | 運営の手動実行 |
| ExecuteOnContestantInstance(s) | FR-S-6 / FR-S-7 |
| LongRunningCheck（clar 2 分滞留・ベンチ 150 秒超を Slack 警告） | **notifyloop が 60 秒間隔で常駐実行** |
| SlackWebhook / DiscordAnnounce / FireTestNotification / ClearContestData | 各呼び出し元 |

Discord bot（常駐）: アナウンス投稿の中継（app 複数台時の connect 問題対応）とロール管理。

## 3. 互換契約（CT）— 変更禁止

| ID | 契約 |
|---|---|
| CT-1 | protobuf API 54 本。`Content-Type: application/protobuf; proto=<full message name>`。エラーも `isuxportal.proto.Error`（human_message + human_descriptions。バリデーションは全件列挙）で 404/400/403/422 |
| CT-2 | SPA シェルの meta タグ。`isux:*` 6 種に加え **`csrf-param` / `csrf-token`**（`csrf_meta_tags` 相当）。**ApiClient は全 mutating リクエストに `X-Csrf-Token` ヘッダを付けて送る**（`ApiClient.ts:327` で確認）。Go はトークンを発行・検証すること。bench / envcheck は対象外 |
| CT-3 | **アセット解決は `public/packs/manifest.json` 経由**（simpacker / webpack-assets-manifest）。シェルはパック名（audience / contestant / admin / broadcast_view / vendor / application / sw）をマニフェストでハッシュ付きファイル名に解決して出力する。長期キャッシュヘッダ（`public,max-age=31536000`）を維持 |
| CT-4 | **Web Push ペイロード = base64(Notification の protobuf バイナリ)**。sw.ts が `atob` → decode する（`sw.ts:142` で確認）。`notifications.encoded_message` カラムも同形式 |
| CT-5 | ETag / 304: ダッシュボード 3 系統は事前生成キャッシュの **SHA-384 ダイジェストを ETag** に、キャッシュミス時は合成 etag（`leaderboard_etag`）/ 自チーム最新結果の updated_at にフォールバックし、`If-None-Match` 一致で 304（Rails `stale?` 相当）。ポーリング負荷対策の中核 |
| CT-6 | 非 admin へのマスク: `stderr=""` / `exit_status=-2` / `exit_signal=-2` / `signaled=false` / `instance_name=""` / `post_validation` 省略。admin は実値（nil は -1） |
| CT-7 | **score センチネル**: 報告の `score == -1` は「`raw - deduction` で計算せよ」。加えて `finished && !passed` は score=0 |
| CT-8 | DB スキーマ 16 テーブル: **既存カラム・インデックスの変更禁止。追加は可**（同意記録 4 カラムはデータ移行で追加する。Rails は未知カラムを無視するので差分テストと両立） |
| CT-9 | 変則ルート `GET /api/admin/instance_command_execute_requests/:request_id/:id/output` を含む全 107 ルートのパス互換 |
| CT-10 | VAPID 鍵は既存形式（`ISUXPORTAL_VAPID_PRIVATE_KEY` = JSON 文字列化した PEM）を読めること。読めない場合は鍵の再発行 + 全購読の再登録が必要になるため、まず互換を試す |
| CT-11 | 環境変数 `ISUXPORTAL_*` の名前と形式を維持（`ISUXPORTAL_FINAL` は廃止）。時刻は ISO8601（`Time.xmlschema` 互換）。全量は `config/environments/development.rb:70-137` |

## 4. データ要件（DM）

| ID | 要件 |
|---|---|
| DM-1 | 時刻はすべて **UTC**（Rails は `config.time_zone` 未設定 = UTC。DB の datetime も UTC）。表示側の JST 変換はフロントの責務 |
| DM-2 | enum の整数値: BenchmarkJob.status = PENDING 0 / RUNNING 1 / ERRORED 2 / CANCELLED 3 / FINISHED 4。ContestantInstance.status = UNKNOWN 0 〜 TERMINATED 5。proto と DB で共通 |
| DM-3 | BenchmarkResult の不変条件: finished 後は変更禁止 / `marked_at` 単調増加 / team・job 変更禁止 / finished なら passed 非 nil。**Go ではアプリ層でなくトランザクション内の検証として実装** |
| DM-4 | 重複 enqueue 防止は Rails ではアプリ層 COUNT のみ（競合で抜けうる）。Go は `SELECT ... FOR UPDATE` で塞ぐ |
| DM-5 | `teams.student` は「メンバー 1 人以上かつ全員学生」で導出（nil 時に自動計算） |
| DM-6 | `handle` / `invite_token` は `SecureRandom.urlsafe_base64(64)` 相当（86 文字）。`handle` は instance_name 設定時のみ生成 |
| DM-7 | 可視性スコープ: audience = 凍結前かつ終了前 / contestant = 自チームは無条件・他チームは凍結前 / admin = 無制限。`contest_freeze` / `contest_end` 未設定時は素通し |
| DM-8 | 非正規化 3 カラム（best_benchmark_result_id / _for_audience_id / _for_admin_id）はベンチ確定ごとに更新。leaderboard の best_score はこのカラムを読む |
| DM-9 | `extra_time_assignments` は `final=false` 固定で読み書き（P-3。スキーマは触らない） |
| DM-10 | Redis 4 系統: セッション / 短期キャッシュ（leaderboard 30 秒）/ `dashboard-v2:*` / `leaderboard-v2:*`。Go は `round` セグメントを落とすため **Rails 並走期間は DB index を分離** |

## 5. 非機能要件（NF）

規模の前提: **登録上限 935 チーム、選手最大 約 2,800 人**（isucon14 の想定 600 チームより大きい。ここが既存実装の実績を超える唯一の点）。

| ID | 要件 |
|---|---|
| NF-1 | ポーリング負荷に耐える。実測値: ダッシュボード 10 秒 / 通知 20 秒 / ジョブ詳細 5 秒 / 配信画面 2.5 秒。935 チーム全員が滞在した場合ダッシュボード + 通知だけで**約 420 req/s、大半は 304**。304 経路（CT-5）を Redis 読みだけで返せること |
| NF-2 | leaderboard 全体生成（935 チーム、history なし）が**数秒以内**。ベンチ確定のたびに走る。リハーサルで実測する（NF-7） |
| NF-3 | ベンチ払い出しの排他が supervisor フリート同時ポーリング下で正しく 1 件ずつ払い出されること |
| NF-4 | 公開 clarification の全選手通知（〜2,800 行 insert）が 150 件バッチで数秒以内 |
| NF-5 | graceful shutdown: SIGTERM でヘルスを落とし、処理中リクエストと実行中ジョブを完了してから終了（ecspresso のローリング更新前提） |
| NF-6 | 観測性: `/site/sha`（git SHA）、Sentry、Prometheus `/metrics`（内部ポート）。構造化ログ（slog） |
| NF-7 | リハーサルは**本番想定規模（900+ チーム相当のデータ）で実施**。数十チームでは NF-1 / NF-2 を検証できない |
| NF-8 | 秘密情報は既存の sops 運用を踏襲。トークン比較は定数時間。セッション Cookie は署名 + Secure + HttpOnly |

## 6. 受け入れ条件

1. HTTP 化した `contest_spec`（465 行 + `history: false` 追加分）が `TARGET_BASE=Go` で全部緑
2. 差分テスト: Rails と Go を同一 DB / 別 Redis index で並走させ、54 API のデコード後比較が一致（マスク値 CT-6・センチネル CT-7 を含む）
3. 実 supervisor + dummybench で enqueue → 払い出し → 途中報告 → 確定 → best 更新 → ダッシュボード反映 → Push 到達（実機）が一巡
4. 既存 isucon-env-checker バイナリが**無改修で** FR-E の 3 本を通せる
5. 既存 SPA が**無改修で**全 25 画面動作（CSRF・manifest 解決・meta タグを含む）
6. NF-1 / NF-2 を 900+ チーム規模のデータで実測しクリア

## 7. 未確定（OQ）

計画書 §12 から更新。**解決済み: ETag の突き合わせ位置（→ CT-5）/ ポーリング間隔（→ NF-1）。**

| ID | 論点 | 潰し方 |
|---|---|---|
| OQ-1 | 体制（1 人か 2 人か）。スケジュールの前提 | 運営判断 |
| OQ-2 | データ移行と Rails→Go 切り替えの順序 | 運営判断 |
| OQ-3 | `survey_response` の粒度（proto は結果単位、DB はチーム単位で上書き） | 実害なしと推定。Rails の挙動に合わせて写す |
| OQ-4 | `instance_name` が ECS Fargate 上で何になるか（孤児回収の識別子。タスク再起動で変わると回収漏れ） | supervisor の taskdef / entrypoint.sh を確認 |
| OQ-5 | CFn テンプレートに渡す変数の全量 | `cf_templates/*.yaml.erb` を読む（ISUCON2026 用に作り直す際の入力仕様） |
| OQ-6 | Discord の `support_comm_channel_roles` 構成 | 運営の Discord 設計 |
| OQ-7 | New Relic 前提の taskdef 環境変数 | `ecs/taskdef/lib/app.libsonnet` を読む |
| OQ-8 | `ISUXPORTAL_ADMIN_ONLY` をいつ落とすかの運用手順 | 運営手順として文書化 |
| OQ-9 | **ALB ヘルスチェックのパス**（Rails 側の設定が terraform / taskdef のどこにあるか未確認） | terraform module を読む |
| OQ-10 | **静的アセットの配信主体**（前段 nginx が `/packs` を直接返すか、app が返すか。CT-3 の実装位置が変わる） | `ecs/nginx/default.conf` を読む |
| OQ-11 | Redis のキー全量（4 系統以外が無いか） | `Rails.cache` / `redis` の全参照を洗う |

OQ-9 / OQ-10 は Phase 1 の着手前に、OQ-4 / OQ-5 は Phase 4 の着手前に潰す。

## 8. 計画書への訂正

本書の洗い直しで判明した、[`portal-go-rewrite.md`](portal-go-rewrite.md) の要修正点。

1. **CSRF が契約から抜けていた**（CT-2）。計画書 §3 は「bench / envcheck は CSRF なし」とだけ書いていたが、
   **逆側**——SPA が `X-Csrf-Token` を送り、シェルが `csrf-token` meta を出す——が要件。
   落とすと mutating API が全部無防備になるか、検証を入れた瞬間にフロントが 4xx で死ぬかのどちらか
2. **meta タグは 6 個ではなく 8 個**（`csrf-param` / `csrf-token` を含む）
3. **アセットは manifest.json 解決**（CT-3）。`html/template` に静的に script タグを書くとビルドのたびに壊れる
4. **Push ペイロードの形式が契約**（CT-4）。sw.js を触らない以上、base64(pb) 固定
5. **スキーマ「変更しない」は「既存の変更禁止・追加は可」に修正**（CT-8）。同意記録 4 カラムの追加と矛盾していた
6. **Phase 7 の「数十チーム規模」は誤り**。登録上限 935 に対して 1 桁足りない。900+ 規模で実測する（NF-7）
