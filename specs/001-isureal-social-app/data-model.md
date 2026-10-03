# Data Model: ISUReal Social Application

## Conventions

- 識別子はUUID、時刻はUTCの`timestamp with time zone`、本文文字数はUnicodeコードポイントで検証する。
- `created_at`と`updated_at`の同値時はUUIDをタイブレーカーにする。公開カーソルは値とIDを含む不透明トークンとする。
- DB列挙値はCHECK制約で固定し、外部キーは完全削除時の順序が明確になるよう原則`RESTRICT`、削除サービスで明示削除する。
- 画像は`image_objects`の不変オブジェクトを参照する。入力ファイルを直接参照せず、再エンコード済みJPEGだけを登録する。
- 通常取得では`users.status = 'active'`を必須とし、ブロックがどちらか一方向でも存在すれば両者間を不可視にする。

## Entity Relationship Overview

```text
User ─1─ Profile
 │├──< LoginSession ─0..1─ BrowserPushSubscription
 │├──< FriendRequest >── User
 │├──< Friendship >───── User
 │├──< Follow >───────── User
 │├──< BlockRelation >── User
 │└──< UserPostingWindowState >── PostingWindow ─1─ NotificationSchedule(region)
 │                              │
 │                              └──< Post ── ImageObject
 │                                   ├──< RealMoji ── ImageObject
 │                                   └──< PostComment
 └──< Notification >──────────── PostingWindow

CallerScope (User/Administrator/UnauthenticatedCredential) ──< IdempotencyRecord
```

## Entities

### users

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | ユーザーID |
| username | varchar(64) | 表示用、`[A-Za-z0-9_.-]{1,64}`、変更不可 |
| username_normalized | varchar(64) UNIQUE | ASCII大文字を小文字化、完全削除まで占有 |
| secret_key_hash | text | ユーザー作成時に発行するSecretKeyの照合用の値。平文は保存しない |
| display_name | text | 1〜100コードポイント |
| display_name_search | text | NFC + Unicode Default Case Folding |
| bio | text | 0〜500コードポイント |
| profile_image_id | UUID NULL FK image_objects | 任意の2 MiB以下JPEG |
| region | enum | `america`, `europe`, `asia` |
| pending_region | enum NULL | 進行中Window終了時の切替先。現在地域指定でNULL |
| account_type | enum | `regular`, `certified_public`。後者から戻せない |
| status | enum | `active`, `deletion_pending`, `purging` |
| deletion_requested_at | timestamptz NULL | 削除要求受理時刻 |
| purge_due_at | timestamptz NULL | 要求時刻+30日 |
| created_at, updated_at | timestamptz | 監査用 |

**Invariants**

- `deletion_pending`または`purging`なら削除2時刻は必須、`active`なら両方NULL。
- 認定公開化は`regular -> certified_public`のみ。ユーザー名は更新APIを持たない。
- `pending_region`はNULLまたは`region`と異なる。Windowがなければ地域を即時変更する。
- 期限到来時に`purging`へ遷移して復旧を永久に拒否する。関連DB行とアカウント専用画像の物理削除完了後に
  ユーザー行を削除してUNIQUE占有を解放し、同名を再登録できる。

### administrators

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 管理者ID |
| login_name_normalized | varchar UNIQUE | 一般ユーザー名前空間と別 |
| password_hash | varchar | 管理者資格情報 |
| enabled | boolean | 無効管理者を拒否 |
| created_at | timestamptz | 作成時刻 |

管理者セッションはユーザーセッションと別テーブル/トークン種別にし、管理APIでのみ受理する。

### image_objects

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 画像参照ID |
| owner_user_id | UUID | 完全削除単位となる所有ユーザー。循環参照を避け削除サービスが整合性を管理 |
| sha256 | char(64) | サニタイズ済みバイトの内容ハッシュ。ユーザー間で物理ファイルを共有しない |
| object_key | text UNIQUE | 配信可能な不変ファイルキー |
| byte_size | bigint CHECK > 0 | 再エンコード後容量 |
| media_type | text CHECK = `image/jpeg` | 固定形式 |
| created_at | timestamptz | 確定時刻 |

入力は用途別上限（投稿10 MiB、プロフィール/RealMoji 2 MiB）を超える前に中断する。デコードと再エンコードは
APIとは別の一回限りのサニタイザプロセスで行い、同時実行枠と設定可能なメモリ、CPU、処理時間上限を適用する。
枠不足、資源上限到達、異常終了、タイムアウトでは503として一時ファイルを回収し、`image_objects`行と公開ファイルを作らない。
これらの上限を画素数または縦横寸法の固定的な受理制約へ変換しない。保存画像は再デコード可能で、
入力メタデータを持たないことを検査する。通常の参照解除では未参照オブジェクトをGC可能とするが、
アカウント完全削除では所有画像を同期削除し、定期GCへ委ねない。

### friend_requests

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 関係ID |
| requester_id, recipient_id | UUID FK users | 異なるユーザー |
| pair_low_id, pair_high_id | UUID | 無向ペアの正規化キー |
| status | enum | `pending`, `accepted`, `rejected`, `cancelled`, `mutualized` |
| created_at, updated_at | timestamptz | 一覧はupdated_at DESC, id DESC |

部分一意制約相当で同一ペアの`pending`を最大1件にする。終了済み履歴は保持し、再申請は新IDで作る。
受信者だけがaccept/reject、申請者だけがcancelできる。逆方向pending作成は既存申請を`mutualized`にし、friendshipを作る。

### friendships

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 関係ID |
| user_low_id, user_high_id | UUID FK users, UNIQUE pair | 並び替え済み無向ペア |
| source_request_id | UUID FK friend_requests | 成立元 |
| created_at, updated_at | timestamptz | 関係一覧順 |

どちらも解除可能。削除は一件の無向関係を消す。ブロック作成と同じトランザクションで削除する。

### follows

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 関係ID |
| follower_id, followee_id | UUID FK users, UNIQUE | 方向付き、自己参照禁止 |
| created_at, updated_at | timestamptz | 関係一覧順 |

followeeが`certified_public`かつ双方active・非ブロックの場合だけ作る。友達成立後も独立して維持する。

### block_relations

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | ブロックID |
| blocker_id, blocked_id | UUID FK users, UNIQUE | 方向付き、自己参照禁止 |
| created_at | timestamptz | 作成時刻 |

作成時は無向ユーザーペアをロックし、friendship、両方向follow、同ペアpending requestを削除/終了してから確定する。
解除はblockerだけ。相互ブロックは2行で、片方の解除ではもう一方を残す。

### posting_windows

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 投稿期間ID |
| region | enum | 3地域 |
| local_date | date | 開始instantを固定offsetで変換したMemories日付 |
| utc_offset_minutes | smallint | America -300, Europe +60, Asia +540 |
| starts_at | timestamptz | 通知/期間開始 |
| on_time_deadline | timestamptz | `starts_at + 2 minutes` |
| ended_at | timestamptz NULL | 同地域の次Window開始時刻 |
| created_at | timestamptz | 作成時刻 |

`UNIQUE(region, local_date)`。地域ごとに`ended_at IS NULL`は最大1件。新Window作成トランザクションで旧Windowを終了し、
旧地域を持つユーザーのpending regionを適用してから対象ユーザー通知を確定する。

### user_posting_window_states

| Field | Type / Constraint | Meaning |
|---|---|---|
| user_id, posting_window_id | UUID composite PK | ユーザーと期間 |
| active_post_id | UUID NULL | 現在有効な投稿 |
| deletion_count | smallint CHECK 0..2 | 期間内削除回数 |
| posted_at | timestamptz NULL | 最後に成立した投稿 |
| updated_at | timestamptz | 状態更新時刻 |

投稿時に行を作成/ロックする。`active_post_id`がNULLかつ`deletion_count < 2`のみ投稿可能。
削除はactive postをNULLにしcountを増加する。2回目削除と再投稿が競合した場合は同じ行ロック上で削除を優先する。

### posts

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 投稿ID |
| author_id | UUID FK users | 投稿者 |
| posting_window_id | UUID FK | 投稿者地域の現在Window |
| image_id | UUID FK image_objects | 10 MiB以下サニタイズ済みJPEG |
| caption | text | 0〜1000コードポイント |
| visibility | enum | `friends`, `friends_of_friends`, `public` |
| location_kind | enum | `none`, `exact`, `approximate` |
| latitude, longitude | numeric NULL | exact時のみ、有限、-90..90 / -180..180 |
| locality | text NULL | approximate時のみ1〜100コードポイント |
| posted_at | timestamptz | 投稿日時 |
| is_on_time | boolean | deadline以下 |
| late_seconds | bigint | on-timeは0、それ以外はdeadlineとの差 |
| deleted_at | timestamptz NULL | 論理削除/不可視化時刻 |

位置種別ごとの排他的CHECKを置く。有効投稿はstateから一件だけ参照される。画像・位置・投稿時刻は編集不可。
captionとvisibilityはWindow終了前だけ投稿者が変更可能（visibility変更後は次回取得から再認可）。

### realmojis

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | RealMoji ID |
| post_id, reactor_id | UUID FK | 対象と反応者 |
| reaction_type | enum | `like`, `smile`, `surprise`, `heart`, `laugh`, `instant` |
| image_id | UUID FK image_objects | 2 MiB以下サニタイズ済みJPEG |
| active | boolean | 置換前はfalse |
| created_at, deleted_at | timestamptz | 一覧順/削除 |

`(post_id, reactor_id)`のactive行は最大1件。投稿者自身は禁止。置換では旧行をinactiveにし、新ID/時刻で作る。
一覧はactiveかつ非削除だけ、`created_at DESC, id DESC`。

### post_comments

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | コメントID |
| post_id, author_id | UUID FK | 対象と送信者 |
| body | text | 1〜1000コードポイント、編集不可 |
| created_at, deleted_at | timestamptz | ASC一覧/削除 |

投稿者本人、直接友達、またはpublicで閲覧可能な利用者だけ作成できる。距離2だけでは不可。
削除は送信者または投稿者。取得は`created_at ASC, id ASC`。

### login_sessions / browser_push_subscriptions

`login_sessions`: `id`, `user_id`, `token_hash`, `active`, `created_at`, `invalidated_at`, `reason`。
ユーザーごとのactive行最大1件。自動期限なし。

`browser_push_subscriptions`: `id`, `session_id`, `user_id`, `endpoint`, `p256dh`, `auth_secret`,
`active`, `created_at`, `updated_at`, `invalidated_at`, `reason`。active sessionごと最大1件。
資格情報はログへ出さない。登録/更新は旧行を無効化して新行を確定し、解除/ログアウト/新ログイン/恒久失敗で無効化する。

### notification_schedules

| Field | Type / Constraint | Meaning |
|---|---|---|
| region | enum PK | 地域ごとの単一設定 |
| scheduled_at | timestamptz NULL | 将来の次回時刻 |
| set_by_admin_id | UUID NULL FK | 設定者 |
| revision | bigint | 変更競合検出 |
| updated_at | timestamptz | 更新時刻 |

開始前のみ変更/取消可能。同地域日ですでにWindowがある時刻は設定不可。ワーカー開始時は同じトランザクションでNULLへ戻す。

### notifications / push_attempts

`notifications`: `id`, `recipient_id`, `posting_window_id`, `region`, `created_at`, `read_at`。
`UNIQUE(recipient_id, posting_window_id)`、一覧は`created_at DESC, id DESC`。個別開封を条件付き更新する。

`push_attempts`: `id`, `notification_id`, `subscription_id`, `delivery_token`, `attempt_count`, `status` (`pending`, `retryable`,
`delivered`, `permanent_failure`, `exhausted`), `next_attempt_at`, `last_attempt_at`, `last_error_class`。
`UNIQUE(notification_id)`とし、`notification_id`を論理通知ID、`delivery_token`を全試行で不変な配送識別子とする。
初回+最大3再試行。Service Workerは論理通知IDを通知タグとして重複表示を置換する。本文やendpointをログへ出さない。

### idempotency_records

| Field | Type / Constraint | Meaning |
|---|---|---|
| id | UUID PK | 記録ID |
| caller_scope_type | enum | `user`, `admin`, `user_login`, `admin_login`, `registration` |
| caller_scope_key | text | 認証済みは主体UUID、未認証は認証種別ごとの正規化済みユーザー名 |
| operation | text | API操作種別 |
| key | UUID | 呼出元指定 |
| generation | bigint | 24時間後の再利用世代 |
| request_hash | char(64) | 正規化要求のSHA-256 |
| status | enum | `processing`, `retryable`, `completed`。503では`retryable` |
| response_status, response_body | int, jsonb | 初回結果 |
| finalized_at, expires_at | timestamptz | 確定と+24時間 |

読み取り専用操作と内部ワーカーを除く全状態変更HTTP操作で有効世代をロックし、同hash completedなら保存結果、
異なるhashなら409、processingなら初回完了後の結果を返す。503ではドメイン変更をロールバックして`retryable`とし、
応答と`finalized_at`を保存せず、同じhashの再取得を許可する。キー欠落は400とする。
期限後は新世代として扱い、古い記録は非同期削除できる。

## Authorization Matrix

| Operation | Self | Direct friend | Distance exactly 2 | Any active user | Follower only |
|---|---:|---:|---:|---:|---:|
| Private basic profile | yes | yes | yes | yes | yes |
| Private bio | yes | yes | no | no | no |
| Certified public bio | yes | yes | yes | yes | yes |
| Friends post | own view only | yes | no | no | no |
| Friends-of-friends audience post (inclusive) | own view only | yes | conditional | no | no |
| Public post | own view only | yes | yes | yes | yes |
| Follow feed | n/a | if following + visibility | if following + visibility | if following + public | same |
| Add RealMoji | no | yes | yes if post visible | yes if public | only if visibility allows |
| Add comment | yes | yes | no when distance2 only | yes if public | only if public/direct friend |
| Memories | owner only | no | no | no | no |

全セルに先立ち、双方active、ブロックなし、対象が現行かつ非削除、閲覧者が自身の地域の現在Windowへ投稿済み、
という該当操作の共通ゲートを評価する。自分の投稿は4フィードから除外し専用取得だけ許可する。
`Friends-of-friends audience`は直接の友達と友達距離2を含む公開範囲を表す。これとは別に、
`friends-of-friends feed`は直接の友達を除外した友達距離2だけを返す。

## State Transitions

### Account

```text
active --delete request--> deletion_pending --valid login before due--> active
                                      └--due reached--> purging --DB + owned images removed--> permanently removed
regular --admin certification--> certified_public (no reverse transition)
```

### Posting window and region

```text
schedule unset --admin set/change--> scheduled --admin cancel--> unset
scheduled --worker at due--> posting_window active + schedule unset
previous active window --next window starts--> ended + pending regions applied
```

### Post slot

```text
empty(count=0) -> posted -> deleted(count=1) -> reposted -> deleted(count=2) -> closed
```

Window終了はどの状態からも新規投稿/編集/交流を閉じ、既存投稿をowner-only Memoriesへ移す。

### Friend request

```text
pending --recipient accepts--> accepted + friendship
pending --recipient rejects--> rejected
pending --requester cancels--> cancelled
pending --reverse request--> mutualized + friendship
friendship --either removes--> removed (historical request unchanged)
```

## Transaction and Lock Ordering

デッドロック回避のため、複数対象は常にUUID昇順でロックする。

1. 冪等性レコード
2. principal user/admin
3. 関係するuser行（昇順）
4. posting_window / user_posting_window_state
5. post
6. relationship / interaction rows
7. notification / subscription

ブロックは同一トランザクション内で関係削除まで行い、他操作はユーザー行ロック後にブロックを再確認する。
完全削除と復旧/ログイン/同名登録は対象user行で直列化し、`now >= purge_due_at`なら`purging`への遷移と
同期削除を優先する。稼働中は期限から60秒以内、期限後のアクセス時は応答前に削除を完了する。

## Indexes Required for Correct Ordering

- `posts(posting_window_id, posted_at DESC, id DESC)`および`posts(author_id, posted_at DESC, id DESC)`
- `users(username_normalized, id)`、検索用`display_name_search`の適切な部分一致索引は実装時に選定
- 各関係の主体列 + `(updated_at DESC, id DESC)`
- `post_comments(post_id, created_at ASC, id ASC)`
- `realmojis(post_id, active, created_at DESC, id DESC)`
- `notifications(recipient_id, created_at DESC, id DESC)`
- `posting_windows(region, ended_at, starts_at DESC)`
- `push_attempts(status, next_attempt_at)`、`users(status, purge_due_at)`

索引の追加/変更は結果順・認可・一意性を変えてはならない。性能課題としての調整は別競技仕様で管理する。
