# isucon2026-portal → isuxportal の登録データ移行

調査日: 2026-08-21。

以下はRails版を候補としていた時点の調査です。
採用したGo版の移行範囲と切替手順は [ポータル切替手順](../../research/portal-cutover.md) を参照してください。

`isucon2026-portal`（Django / TiDB）は**すでに本番稼働している**。
`deploy-prod.yml` が `app-001.portal.isucon.net` / `app-002.portal.isucon.net` の
2 台にローリングアップデートしており、登録開始は 2026-07-28、
段階募集の第 3 期（935 枠）は 8-05 に開いている。**登録済みチームのデータが本番 DB にある。**

isuxportal（Rails / MySQL）に切り替えるなら、スキーマ変換が必要になる。以下はフィールド単位の対応。

## フィールド対応表

| isucon2026-portal（Django） | isuxportal（Rails） | 移行の可否と注意 |
|---|---|---|
| `User.username` | `contestants.github_login` | そのまま。social auth が GitHub の login を username にしている |
| `social_auth_usersocialauth.uid` | `contestants.github_id` **NOT NULL** | **social_django のテーブルから引ける。GitHub API への再問い合わせは不要** |
| `User.display_name` | `contestants.name` | そのまま |
| `User.is_student` | `contestants.student` | そのまま |
| `User.discord_id` | `contestants.discord_id` **NOT NULL / UNIQUE** | Discord 未連携ユーザーは空文字。**空文字が複数あると UNIQUE 制約に衝突する**。移行前に連携状況を確認 |
| `User.discord_username` | `contestants.discord_tag` **NOT NULL** | Discord は discriminator を廃止済みなので username をそのまま入れる |
| `User.icon`（StdImage） | `contestants.avatar_url` **NOT NULL** | **実体の移動が必要**。さくらのオブジェクトストレージ → S3 へコピーし URL を書き換える。isucon14 の `Avatar` モジュールと同じバケットに置けばよい |
| `User.email` | `teams.email_address` **NOT NULL** | 代表者の email をチームに移す。登録フォームで必須収集済み |
| `User.terms_agreed_at`<br>`privacy_agreed_at`<br>`sponsor_provide_agreed_at`<br>`minor_parental_consent` | **対応カラムなし** | **isuxportal 側に 4 カラムを追加するマイグレーションが必要。法的な同意記録なので捨てられない** |
| `Team.name` | `teams.name` | そのまま |
| `Team.owner`（User） | `teams.leader_id`（contestants.id） | **順序依存**。contestants を先に投入して id を確定させる 2 パス構成にする |
| `Team.password` | **対応なし**（`teams.invite_token`） | **参加方式が違う**。2026 はチームパスワードの共有、isuxportal は招待 URL。移行時に invite_token を生成し**選手へ再案内が必要** |
| `Team.declined_at` | `teams.withdrawn` | 日時 → 真偽値。**辞退日時は失われる**（残すならカラム追加） |
| `Team.banned_at` | `teams.disqualified` | 同上 |
| `Team.is_guest` | `teams.is_hidden` | 意味が近い（スポンサー枠を順位表から隠す）。運用意図の確認が必要 |
| `Team.is_local_participation` | `contestants.in_person`（isucon14 のみ） | **粒度が違う**。2026 はチーム単位、isucon14 は個人単位。チームの値を全メンバーに展開する |
| `Team.want_local_participation` | **対応なし** | 「希望」と「確定」の 2 段が isuxportal に無い。カラム追加か運用で吸収 |
| `Team.aws_az` | `teams.availability_zone` | **値の形式が同じ**（`apne1-az1` 等）。そのまま入る。isucon14 は既定値 `apne1-az2` を持つ |
| `Team.sacloud_coupon`<br>`Team.aws_coupon` | `coupons.code` | isuxportal は 1 チーム 1 レコードで `code` は repeated。**2 種類を 1 レコードにまとめるか 2 レコードに分けるかの設計判断** |
| `Team.envcheck_token` | `CheckerToken` | isuxportal 側は期限付き。移行時に `expiry` を設定する |
| **該当なし** | `teams.student` **NOT NULL** | **導出規則は isuxportal 側のコードにある**。`members.size > 0 && members.all?(&:student?)`（`Team#members_all_student?`）。移行時も同じ規則で埋めればよい |
| `RegisterCoupon` | **対応なし** | 招待制の登録クーポンは isuxportal に無い。残すなら新テーブル。**登録が締まっていれば移行不要** |
| `envcheck.EnvCheckResult` | `env_checks` | **ほぼ 1:1**（name / passed / ip_address / message / admin_message / raw_data） |
| `contact.Ticket`<br>`TicketComment` | `clarifications` | **スレッドが 1 問 1 答に潰れる**。ジャンル・公開範囲 3 段も落ちる |
| `contest.Information` | `clarifications`（`admin=true`） | お知らせを運営発信 clarification として移す |
| `contest.Server` | `contestant_instances` | `cloud_id` NOT NULL と `number` 1..3 が必須。2026 は hostname ベースで number を持たない。**当日前なら空なので移行不要の見込み** |
| `contest.Job` / `Score` | `benchmark_jobs` / `benchmark_results` | 同上。**当日前なら空**。`Score` は isuxportal では `teams` の非正規化カラムに相当 |

## 障害は 4 つ

### 一. 参加方式の変更が選手に見える

チームパスワード共有から招待 URL に変わる。登録期間は 9-30 まで開いているので、
**期間中の切り替えは避け、締切後から当日（11-25）までの間に行う**のが安全。

### 二. 同意記録 4 カラムの追加

参加規約・個人情報・スポンサー提供・未成年の法定代理人同意は
isuxportal のスキーマに存在しない。登録フォーム（`TeamRegisterForm`）で
`agree_terms` / `agree_privacy` / `agree_sponsor` / `minor_parental_consent` を
必須収集しており、法的記録なので捨てられない。カラム追加と管理画面の表示が必要。

### 三. アイコンの実体移動

登録フォームはアイコンを必須にしている（`is_import_github_icon` か `user_icon` のどちらか）
ため、全チーム分が存在する。さくらのオブジェクトストレージから S3 へコピーする。

### 四.（解消）`teams.student` の導出規則

当初は運営判断が要ると考えたが、**isuxportal のコードに答えがあった**。
`Team#members_all_student?` が `members.size > 0 && members.all?(&:student?)` で、
`before_validation` により `student` カラムが nil のとき自動計算される。
移行時も同じ規則で埋めればよく、判断は不要。

## 移行が楽な点

- **GitHub の数値 ID は再取得不要**。`social_auth_usersocialauth.uid` から引ける
- **`aws_az` の値形式が一致**。`apne1-az1` 形式で `availability_zone` にそのまま入る
- **`EnvCheckResult` はほぼ 1:1**
- **`Server` / `Job` / `Score` は当日前なら空**なので移行不要の見込み

## 作業量の非対称性

移行コストが発生するのは **isuxportal を採る場合だけ**。
`isucon2026-portal` を拡張する選択肢ではゼロ。
逆に `isucon2026-portal` では当日機能
（leaderboard 3 系統・凍結・通知・追加時間・impersonate）を新規実装する。

**どちらに労力を払うかの選択である。**
