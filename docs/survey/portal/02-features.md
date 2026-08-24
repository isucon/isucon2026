# ポータル機能台帳

調査日: 2026-08-21。実装から抽出した 125 機能を
`isucon/portal`（2022）と `isucon2026-portal` で突き合わせた。

凡例: `●` 実装あり / `◐` 部分的・要改修 / `○` なし / `—` 設計上不要 /
難度 `L` `M` `H` は Go 移植の難度 / `★` は `isucon14-portal` で追加・改善された項目。

集計:

| | 件数 |
|---|---:|
| 台帳の機能 | 125 |
| `isucon/portal` に実装あり | 104（ほか 9 件が部分実装） |
| `isucon14-portal` に実装あり | 107（＋台帳外の新機能 6 件） |
| `isucon2026-portal` に実装あり | 77（ほか 20 件が部分実装） |
| 両系統ともに欠けている行 | **0** |

**二つの実装の和は機能要件をほぼ覆っている。**
真に足りないのは環境側の項目のみ（`05-go-backend-rewrite.md` および `04-data-migration.md` 参照）。

---

## A. 認証・セッション（7）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| GitHub OAuth ログイン | ● | ● | L | portal は `omniauth-github`、2026 は `social-auth-app-django`。Go なら `x/oauth2` |
| Discord OAuth ログイン | ● | ● | L | portal は登録時に GitHub と Discord の**両方が必須** |
| セッション永続化 | ● | ● | L | portal は `redis-actionpack`（Redis 必須） |
| 運営ログイン | ● | ● | L | portal は `ISUXPORTAL_ADMIN_LOGIN/PASSWORD`、2026 は Django admin |
| impersonate（選手なりすまし） | ● | ○ | M | contestant / github / discord の 3 経路。当日の問い合わせ対応で効く |
| bypass token | ● | ○ | M | HMAC-SHA384 署名 + 期限。用途は `CREATE_TEAM / JOIN_TEAM / HIDDEN_TEAM / LEAVE_TEAM` |
| 現在セッション取得 API | ● | — | L | SPA 前提。`GetCurrentSession` がチーム・競技時刻・インスタンス・VAPID 鍵を一括で返す |

## B. 参加登録（21）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| チーム作成 | ● | ● | L | portal は作成時に AZ を `team.id % zones.size` で自動割当 |
| チーム数上限の排他制御 | ◐ | ● | M | portal はトランザクション内チェックのみで競合しうる。2026 は `GET_LOCK('create_team')` で直列化 |
| 招待トークンでチーム参加 | ● | ● | L | portal は `Team#joinable?` + 招待締切を別管理 |
| 招待 URL 発行 | ● | ● | L | |
| チーム情報の更新 | ● | ● | L | 両者ともチーム名変更は代表者のみ。portal は競技開始後は不可 |
| 参加辞退（チーム解散） | ● | ● | L | portal は代表者の削除で `withdrawn` が立ち全員 destroy |
| メンバー個別の脱退 | ● | ● | L | 2026 はごく最近追加（`49105f2`） |
| 学生フラグ | ● | ● | L | |
| 登録期間の制御 | ● | ● | L | portal は open / close / invitation_close / update_close の 4 期限を独立に持つ |
| 段階的な募集枠 | ○ | ● | M | 2026 独自。`TEAM_NUM_SCHEDULE` で「時刻 → 累計上限」を段階指定（300 / 600 / 935） |
| 最大チーム数上限 | ● | ● | L | |
| 規約・個人情報の同意記録 | ○ | ● | L | 2026 独自。参加規約 / 個人情報 / スポンサー提供（任意）/ 未成年の法定代理人同意を日時付きで保存 |
| 表示名・アイコンアップロード | ◐ | ● | M | ★ portal は GitHub の avatar_url を流用するのみ。isucon14 で S3 presigned + `react-avatar-editor` を追加 |
| 現地参加の希望・確定 | ○ | ● | L | ★ isucon14 で `contestants.in_person` を追加。2026 は Discord の現地参加ロールに連動 |
| クラウドクーポン配布 | ● | ● | L | portal は `Coupon` + `ActivateCoupon`。2026 はさくら / AWS の 2 種を保持 |
| 招待制の登録クーポン | ○ | ● | L | 2026 独自。`RegisterCoupon` で枠外の個別招待 |
| チーム一覧の公開 | ● | ● | L | |
| チーム一覧 CSV エクスポート | ◐ | ● | L | portal は `dump_leaderboard` のみ。2026 は `teams.csv` + django-import-export |
| Discord サーバ自動 join | ● | ● | M | OAuth の access_token で guild member add |
| Discord ロール・ニックネーム同期 | ● | ● | M | portal は選手 / 予選通過 / 本選のロールを個別管理。2026 は 403 時にロールのみ再試行 |
| Discord 参加状態の一括同期 | ● | ● | M | portal は全選手を回すジョブ + 統計。2026 は `updatediscord` コマンド |

## C. 環境チェック（envcheck）（8）

競技環境が AWS に確定したため、この領域はほぼ無改修で流用できる。
残るのは ISUCON2026 用 AMI とリソース定義の差し替え。

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| 環境テンプレートの動的生成 | ● | ● | M | portal は ERB で `test.yaml` / `qualify.yaml` を生成。**競技環境が AWS なので枠組みはそのまま流用可** |
| チェッカー用トークン発行 | ● | ● | L | portal は `CheckerToken` に期限付き。テンプレートに base64 で埋め込む |
| チェック結果の受信 API | ● | ● | L | portal `POST /api/env_checks`、2026 `POST /envcheck/result/` |
| チェック進捗の表示 | ● | ◐ | L | portal は `NOT_STARTED / CREATED_INSTANCE / DONE` の 3 状態。2026 は現在**非表示** |
| 運営向け結果閲覧 | ● | ● | L | portal は選手向け `message` と運営向け `admin_message` を分離。生データも保持 |
| SSH 公開鍵の収集 | ● | ● | L | `github.com/<user>.keys` から取得。portal は専用ジョブ + 統計 |
| SSH 公開鍵の配布 API | ● | ○ | L | portal `GET /api/ssh_public_keys`。競技サーバ構築時に鍵を流し込む |
| envcheck 実行バイナリ | ● | ● | M | 両者とも Go 実装（`isucon-env-checker`）。**すでに Go なので流用可能** |

## D. 競技サーバ管理（6）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| 選手インスタンス一覧 | ● | ● | L | portal `ContestantInstance`（1〜3 番、状態 6 種）、2026 `Server` |
| インスタンス状態の自動同期 | ● | ○ | L | portal は EC2 API をタグ（`IsuconTeamID` / `IsuconInstanceNum`）で引く。**競技環境が AWS なので無改修で流用可能**。ただし envcheck 経由の第 2 経路もある（`03-routes.md` §6 参照） |
| サーバの手動登録・削除 | ○ | ● | L | 2026 は選手自身が IP を登録 |
| ベンチマーク対象の選択 | ● | ● | L | portal は enqueue 時に `target_id`。2026 は `is_bench_target` フラグ |
| AZ / ゾーンの割り当て | ● | ● | L | 2026 は AZ ごとに SQS キューを分けている |
| サーバの一括再起動指示 | ○ | ● | M | ★ isucon14 は `TriggerInstanceRestart` で SSH 経由の `sudo reboot` を一括投入。2026 は `send_reboot` |

## E. ベンチマーク（19）

portal は **pull 型**（supervisor がジョブを取りに来る）、
2026 は **push 型**（SQS に投げ、結果を別プロセスが拾う）。

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| 選手によるジョブ enqueue | ● | ● | L | |
| 運営によるジョブ enqueue | ● | ◐ | L | ★ isucon14 は `enqueued_by_id` で実行者を記録し、`post_validation` 付き一括投入も持つ。2026 は `run_test_jobs` |
| 重複 enqueue の防止 | ● | ● | L | 2026 は `DuplicateJobError` を明示。portal は status で判定 |
| ジョブ一覧（選手） | ● | ● | L | |
| ジョブ一覧（運営・絞り込み） | ● | ● | M | portal は team_id / status / `failed_only` + ページング |
| ジョブ詳細 | ● | ● | L | stdout / stderr / reason / exit_status / exit_signal / signaled |
| ジョブのキャンセル（運営） | ● | ◐ | L | 2026 は `CANCELED` 状態を持つが UI 経路が無い |
| ベンチへのジョブ払い出し | ● | ◐ | H | portal `ReceiveBenchmarkJob`（行ロックで取り合う）。2026 は SQS 送信のみで **dequeue API が無効化** |
| ベンチ専用 / 共用の切替 | ◐ | ◐ | M | portal は `team_id > 0` で専用化できるが `TODO` コメントが残る。2026 は IP で紐付ける設計（文書のみ） |
| 途中経過の報告 | ● | ○ | M | portal `ReportBenchmarkResult`。`marked_at` が新しいものだけ採用する順序制御あり |
| 確定結果の報告 | ● | ◐ | M | portal `CompleteBenchmarkJob`。2026 は `receive_job_results` が SQS を常駐ポーリング |
| ベンチマーカーの認証 | ● | ◐ | L | portal は共有トークンを `secure_compare`。2026 は送信元 IP で紐付ける想定 |
| handle によるジョブ所有検証 | ● | ○ | L | 払い出し時の `handle` を報告時に照合。他チームの結果を書けない |
| 孤児ジョブの回収 | ● | ● | M | portal `CancelOwnedBenchmarkJob`（ベンチ再起動時に自分の running を落とす）。2026 は `discard_timeout_jobs` |
| 長時間実行ベンチの検知 | ● | ○ | L | portal は 150 秒超で Slack に警告（`LongRunningCheckJob`） |
| スコア内訳 | ● | ○ | L | portal は `raw` と `deduction`（減点）を分けて保持 |
| 実装言語の申告 | ● | ● | L | portal は `SurveyResponse` をベンチ結果に添付させる |
| supervisor（プロセス監督） | ● | ○ | M | Rust。ベンチ起動 / ハードタイムアウト / fd 経由の報告収集 / ログ保存。**ポータルを Go にしても無改修で使える** |
| ベンチ向け報告ライブラリ | ● | ○ | L | `benchrun`。`ISUXBENCH_TARGET` を読み `Reporter.Report()` を呼ぶだけ。**既に Go** |

### 現行 bench（ISUReal）との接続

いまのベンチマーカーは結果 JSON を標準出力に出し、終了コードで pass / fail を返す。
isuxportal に載せる場合、`benchrun.NewReporter()` を取得して
`BenchmarkResult`（`score` / `passed` / `score_breakdown` / `execution` / `marked_at`）を
組み立てて渡す薄い層を足すだけで済む。
参考実装は `supervisor/dummybench/`。**この一点は isuxportal 系が明確に有利。**

## F. スコア・順位（16）

移植の最難関。portal は選手 / 観客 / 運営の 3 系統で見えるスコアが異なり、
凍結時刻を跨ぐとさらに変わる。この仕様は文書化されておらず、コードから読み取るしかない。

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| 最新スコア / ベストスコア | ● | ◐ | M | 2026 は `Score` モデルがあるが `Team.score` はダミー固定値 |
| 可視性 3 系統のベスト保持 | ● | ○ | **H** | `teams` に `best_benchmark_result_id` を contestant / audience / admin で 3 本非正規化。`MaintainBestBenchmarkResultOfTeamJob` が維持 |
| スコア履歴グラフ | ● | ● | M | portal は履歴を protobuf に載せてフロントで描画。2026 は Redis にグラフデータを pickle 保存 |
| leaderboard（選手向け） | ● | ◐ | **H** | |
| leaderboard（観客向け） | ● | ◐ | **H** | |
| leaderboard（運営向け） | ● | ● | **H** | 運営だけは凍結中も実スコアを見る |
| スコア凍結（freeze） | ● | ◐ | **H** | 2026 は「ラストスパート」として部分グラフ / 全体グラフを分ける別方式 |
| 隠しチームの分離 | ● | ◐ | M | portal は `hidden_teams` として別配列で返す。2026 は `is_guest` |
| 失格・辞退チームの除外 | ● | ● | L | |
| ETag によるキャッシュ制御 | ● | ○ | **H** | チーム数 / 最終更新 / 最新結果 ID を protobuf で束ねた合成 ETag。ポーリング負荷を抑える中核 |
| ダッシュボードの事前生成 | ● | ● | **H** | portal `UpdateContestantDashboardJob` がチーム × 3 可視性ぶんを protobuf で Redis に焼く |
| 追加時間の付与 | ● | ○ | M | portal `ExtraTimeAssignment`。障害時にチーム単位で競技時間を延長できる。**当日の救済手段として重要** |
| 順位表のダンプ | ● | ◐ | M | portal は `when=` に ISO8601 か `qualify-end` を指定して任意時点の確定順位を出せる |
| 結果公開ページ | ◐ | ● | L | 2026 は `SHOW_RESULT_AFTER` 以降に専用の結果画面を開放 |
| チームのピン留め | ● | ○ | L | フロント側の localStorage（上限 20）。サーバ実装は不要 |
| 表彰用の集計 | ○ | ● | L | 2026 の `award` コマンド |

### ここを取り違えると当日に順位が壊れる

`contest.rb` の `leaderboard` は 274 行のうち大半を占める中核で、
生の MySQL ストリーミングクエリ（`cast: true, cache_rows: false, stream: true`）を直接叩いている。
可視性 3 系統 × 凍結の組み合わせ、`teams` の非正規化カラム 3 本、それを維持するジョブ、
合成 ETag、Redis の事前生成キャッシュがひとつの塊として動いている。

**救いは、この 274 行が `isucon/portal` から `isucon14-portal` まで diff ゼロであること。**
ISUCON12 と 14 を無変更で通しており、仕様が固まっている。

## G. 質問・アナウンス（9）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| 質問の投稿（選手） | ● | ● | L | portal は `Clarification`、2026 は `Ticket` + `TicketComment` |
| 回答（運営） | ● | ● | L | |
| 公開 / 非公開の切替 | ● | ● | L | 2026 は公開 / 要約のみ公開 / 非公開の 3 段。portal は `disclosed` の 2 値 |
| 質問文の運営側書き換え | ● | ● | L | portal は `original_question` を残して公開用に整形できる |
| 運営発信のアナウンス | ● | ● | L | portal は `admin` フラグ付き clarification として同じ器で扱う。2026 は `Information` モデル |
| ジャンル分類 | ○ | ● | L | 2026 は 競技用コード / インフラ / レギュレーション / ベンチ / 環境チェック / その他 |
| スレッド形式のやりとり | ○ | ● | M | 2026 は `TicketComment` で往復できる。portal は 1 問 1 答 |
| 未回答の滞留監視 | ● | ○ | L | portal は 2 分以上未回答で Slack に警告 |
| Discord サポートチャンネル連携 | ● | ◐ | M | portal は `SupportCommInfo` でチャンネルと clarification を紐付ける |

## H. 通知（9）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| アプリ内通知の一覧 | ● | ○ | M | portal は protobuf の oneof でベンチ完了 / clarification / テストを表現 |
| 既読管理 | ● | ○ | L | `after` パラメータ優先、無ければサーバ側 `read` カラム |
| 全体通知の一括挿入 | ● | ○ | M | 公開 clarification は全選手へ 150 件ずつ `insert_all!`。数百チーム規模の考慮あり |
| Web Push の購読 / 解除 | ● | ○ | M | VAPID。endpoint / p256dh / auth を保存 |
| Web Push の配信 | ● | ○ | M | 期限切れ購読は自動削除。Go にも実装はあるが検証が必要 |
| Service Worker | ● | ○ | L | `sw/`。フロント資産なので流用可能 |
| Discord へのアナウンス投稿 | ● | ◐ | L | portal は新規 / 更新 / アナウンスで文面を出し分ける |
| Slack への運営通知 | ● | ● | L | portal は新規チーム / 辞退 / ベンチ異常 / 滞留 clarification。2026 は例外ハンドラ経由 |
| テスト通知の発火 | ● | ○ | L | 当日前の疎通確認用 |

## I. 配信・観客（5）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| 観客向けダッシュボード | ● | ◐ | M | portal は個別チームの solo 表示も持つ |
| 観客向けチーム一覧 | ● | ● | L | |
| 配信用 leaderboard 画面 | ● | ● | L | 2026 は Vite + React の別建て（`broadcast/`） |
| 配信用スコア変動画面 | ● | ● | L | |
| 配信用時計画面 | ● | ● | L | |

## J. 運営ツール（12）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| チーム編集（失格・隠し・本選） | ● | ● | M | portal は専用 Admin SPA。2026 は Django admin |
| チーム詳細の閲覧 | ● | ● | L | |
| 運営ダッシュボード | ● | ● | M | portal は未回答 clarification の最古時刻と件数を同時に返す |
| 申込数ダッシュボード | ○ | ● | L | 2026 独自。段階募集の枠消化を見る |
| お知らせ管理 | ◐ | ● | L | portal は clarification で兼用 |
| 競技データの全削除 | ● | ○ | L | portal `ClearContestDataJob`。リハーサル後のリセットに使う |
| ベンチ時刻のシフト | ● | ○ | L | portal `shift_benchmark_time` rake。過去データを別時刻に写して画面確認する |
| テストジョブの一括実行 | ○ | ● | L | 2026 `run_test_jobs` |
| テストデータ生成 | ● | ● | L | portal は factory_bot + faker、2026 は `manufacture` + factory-boy |
| CSV インポート / エクスポート | ○ | ● | L | 2026 は django-import-export |
| デバッグ用エンドポイント群 | ● | ○ | L | ★ portal は HTML の暫定画面。isucon14 で `unprepared_stats` API + 管理画面に昇格 |
| ダミーベンチマーカー | ● | ○ | L | portal `isuxportal-dummy-benchmarker.rb` と `supervisor/dummybench` |

## K. 基盤・運用（13）

| 機能 | portal | 2026 | 難度 | 備考 |
|---|:-:|:-:|:-:|---|
| protobuf による API 契約 | ● | ○ | L | ★ 44 ファイル 1,038 行。isucon14 は `buf` で Go / TS / Ruby / Rust を一括生成。**移植の最大の資産** |
| エラー表現の統一 | ● | ◐ | L | portal は `Error` message に人間向け説明とデバッグ情報を分けて載せる |
| ヘルスチェック | ◐ | ● | L | 2026 は SIGTERM でヘルスを落として LB 離脱を待つ実装まで入っている |
| graceful shutdown | ◐ | ● | M | 2026 の `entrypoint.sh` は 60 秒待ってから gunicorn を落とす |
| エラートラッキング | ● | ● | L | portal は Sentry、2026 は Slack へのログハンドラ |
| メトリクス | ● | ● | L | portal は New Relic、2026 は django-prometheus |
| 非同期ジョブ実行基盤 | ● | ◐ | M | portal 18 ジョブ / isucon14 20 ジョブ。ActiveJob + shoryuken + SQS |
| リビジョン表示 | ● | ● | L | portal `/site/sha`、2026 は `BUILD_HASH` |
| DB マイグレーション | ● | ● | L | Go なら別ツール（golang-migrate 等） |
| 静的アセットビルド | ● | ● | L | ★ portal は webpack + yarn + Node 16。isucon14 は pnpm 9.15 + Node 20 + TS 5.5 |
| コンテナビルド・デプロイ | ● | ● | M | ★ portal は hako、isucon14 は ecspresso + sops(KMS) + ARM64 Fargate |
| Infrastructure as Code | ● | ◐ | M | ★ portal は 58 ファイルのフラット構成。isucon14 は module 化 24 ファイル 1,634 行 |
| 自動テスト | ◐ | ◐ | M | `isucon/portal` も isucon14 も RSpec **818 行で同一**。うち `contest_spec.rb` 465 行が **leaderboard を集中的に検証**しており、残る model spec 11 本は `pending` の 5 行スタブ。**最も危険な部分にだけテストがある** |
