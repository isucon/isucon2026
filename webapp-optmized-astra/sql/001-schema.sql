DROP TABLE IF EXISTS
  astra_history_changes,
  astra_history_epoch,
  astra_search_dirty,
  astra_user_search,
  ip_bans,
  astra_notification_batches,
  notifications,
  comments,
  realmojis,
  friend_requests,
  posts,
  posting_windows,
  app_settings,
  worker_jobs,
  idempotency_records,
  block_relations,
  follows,
  friendships,
  administrator_sessions,
  login_sessions,
  administrators,
  users,
  image_objects;

-- [作問] 画像情報
-- [作問] usernameも当然UniqueだがIndex貼っていないので気を付けること
-- [作問] ログインにはsecretKeyを使う。secret_key_hashはサーバーで生成されたsecretKeyをSHA256ハッシュしたもの。

-- 2026-09-01: 画像をDBにおかないと、別のサーバーからデータを返却したときに404になってしまう。ファイル配置に変更して障害になってしまったので注意。ここは決して変更しないこと。
CREATE TABLE image_objects (
  id varchar(36) NOT NULL,
  owner_user_id varchar(36) NOT NULL,
  data bytea NOT NULL,
  asset_key varchar(64),
  -- like: image/jpeg, image/png
  media_type text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE TABLE image_objects_202409 PARTITION OF image_objects FOR VALUES FROM ('2024-09-01 00:00:00+00') TO ('2024-10-01 00:00:00+00');
CREATE TABLE image_objects_202410 PARTITION OF image_objects FOR VALUES FROM ('2024-10-01 00:00:00+00') TO ('2024-11-01 00:00:00+00');
CREATE TABLE image_objects_202411 PARTITION OF image_objects FOR VALUES FROM ('2024-11-01 00:00:00+00') TO ('2024-12-01 00:00:00+00');
CREATE TABLE image_objects_202412 PARTITION OF image_objects FOR VALUES FROM ('2024-12-01 00:00:00+00') TO ('2025-01-01 00:00:00+00');
CREATE TABLE image_objects_202501 PARTITION OF image_objects FOR VALUES FROM ('2025-01-01 00:00:00+00') TO ('2025-02-01 00:00:00+00');
CREATE TABLE image_objects_202502 PARTITION OF image_objects FOR VALUES FROM ('2025-02-01 00:00:00+00') TO ('2025-03-01 00:00:00+00');
CREATE TABLE image_objects_202503 PARTITION OF image_objects FOR VALUES FROM ('2025-03-01 00:00:00+00') TO ('2025-04-01 00:00:00+00');
CREATE TABLE image_objects_202504 PARTITION OF image_objects FOR VALUES FROM ('2025-04-01 00:00:00+00') TO ('2025-05-01 00:00:00+00');
CREATE TABLE image_objects_202505 PARTITION OF image_objects FOR VALUES FROM ('2025-05-01 00:00:00+00') TO ('2025-06-01 00:00:00+00');
CREATE TABLE image_objects_202506 PARTITION OF image_objects FOR VALUES FROM ('2025-06-01 00:00:00+00') TO ('2025-07-01 00:00:00+00');
CREATE TABLE image_objects_202507 PARTITION OF image_objects FOR VALUES FROM ('2025-07-01 00:00:00+00') TO ('2025-08-01 00:00:00+00');
CREATE TABLE image_objects_202508 PARTITION OF image_objects FOR VALUES FROM ('2025-08-01 00:00:00+00') TO ('2025-09-01 00:00:00+00');
CREATE TABLE image_objects_202509 PARTITION OF image_objects FOR VALUES FROM ('2025-09-01 00:00:00+00') TO ('2025-10-01 00:00:00+00');
CREATE TABLE image_objects_202510 PARTITION OF image_objects FOR VALUES FROM ('2025-10-01 00:00:00+00') TO ('2025-11-01 00:00:00+00');
CREATE TABLE image_objects_202511 PARTITION OF image_objects FOR VALUES FROM ('2025-11-01 00:00:00+00') TO ('2025-12-01 00:00:00+00');
CREATE TABLE image_objects_202512 PARTITION OF image_objects FOR VALUES FROM ('2025-12-01 00:00:00+00') TO ('2026-01-01 00:00:00+00');
CREATE TABLE image_objects_202601 PARTITION OF image_objects FOR VALUES FROM ('2026-01-01 00:00:00+00') TO ('2026-02-01 00:00:00+00');
CREATE TABLE image_objects_202602 PARTITION OF image_objects FOR VALUES FROM ('2026-02-01 00:00:00+00') TO ('2026-03-01 00:00:00+00');
CREATE TABLE image_objects_202603 PARTITION OF image_objects FOR VALUES FROM ('2026-03-01 00:00:00+00') TO ('2026-04-01 00:00:00+00');
CREATE TABLE image_objects_202604 PARTITION OF image_objects FOR VALUES FROM ('2026-04-01 00:00:00+00') TO ('2026-05-01 00:00:00+00');
CREATE TABLE image_objects_202605 PARTITION OF image_objects FOR VALUES FROM ('2026-05-01 00:00:00+00') TO ('2026-06-01 00:00:00+00');
CREATE TABLE image_objects_202606 PARTITION OF image_objects FOR VALUES FROM ('2026-06-01 00:00:00+00') TO ('2026-07-01 00:00:00+00');
CREATE TABLE image_objects_202607 PARTITION OF image_objects FOR VALUES FROM ('2026-07-01 00:00:00+00') TO ('2026-08-01 00:00:00+00');
CREATE TABLE image_objects_202608 PARTITION OF image_objects FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00');
CREATE TABLE image_objects_202609 PARTITION OF image_objects FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');
CREATE TABLE image_objects_202610 PARTITION OF image_objects FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-10-28 06:00:00+00');
CREATE TABLE image_objects_20261028 PARTITION OF image_objects FOR VALUES FROM ('2026-10-28 06:00:00+00') TO ('2026-11-01 00:00:00+00');
CREATE TABLE image_objects_202611 PARTITION OF image_objects FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');
CREATE TABLE image_objects_202612 PARTITION OF image_objects FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE image_objects_202701 PARTITION OF image_objects FOR VALUES FROM ('2027-01-01 00:00:00+00') TO ('2027-02-01 00:00:00+00');
CREATE TABLE image_objects_202702 PARTITION OF image_objects FOR VALUES FROM ('2027-02-01 00:00:00+00') TO ('2027-03-01 00:00:00+00');
CREATE TABLE image_objects_202703 PARTITION OF image_objects FOR VALUES FROM ('2027-03-01 00:00:00+00') TO ('2027-04-01 00:00:00+00');
CREATE TABLE image_objects_202704 PARTITION OF image_objects FOR VALUES FROM ('2027-04-01 00:00:00+00') TO ('2027-05-01 00:00:00+00');
CREATE TABLE image_objects_202705 PARTITION OF image_objects FOR VALUES FROM ('2027-05-01 00:00:00+00') TO ('2027-06-01 00:00:00+00');
CREATE TABLE image_objects_202706 PARTITION OF image_objects FOR VALUES FROM ('2027-06-01 00:00:00+00') TO ('2027-07-01 00:00:00+00');
CREATE TABLE image_objects_default PARTITION OF image_objects DEFAULT;

-- [作問] ユーザーの情報
-- [作問] usernameも当然UniqueだがIndex貼っていないので気を付けること
-- [作問] ログインにはsecretKeyを使う。secret_key_hashはサーバーで生成されたsecretKeyをSHA256ハッシュしたもの。
CREATE TABLE users (
  id varchar(36) PRIMARY KEY,
  username varchar(64) NOT NULL,
  secret_key_hash text NOT NULL,
  display_name text NOT NULL,
  bio text NOT NULL DEFAULT '',
  profile_image_id varchar(36),
  -- america/europe/asia
  region text NOT NULL,
  -- regular/certified_public
  account_type text NOT NULL DEFAULT 'regular',
  -- active/deleted
  status text NOT NULL DEFAULT 'active',
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE image_objects ADD CONSTRAINT image_objects_owner_fk FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE RESTRICT;

-- 2026-09-10 管理者を消す時のオペレーションはどうすればよいですか？
-- 2026-09-13 消すことは想定されていません
CREATE TABLE administrators (
  id varchar(36) PRIMARY KEY,
  username varchar(64) NOT NULL,
  password_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- [作問] ログインしているユーザーのセッション情報。1ユーザーは1ログインのみ有効
-- [作問] idは特に意味なし。user_idはユーザー情報。ユーザー毎に1つしかログインできない
-- [作問] token_hashはサーバーで生成されたトークンをSHA256ハッシュしたもの。
-- [作問] activeはログイン中かどうか。
-- [作問] 他はFlavor。特に使われていない
-- [作問] token_hashにはINDEXがなく、Unique制約もないため、アプリ側でUniqueであることを保証すること。ユーザー単位に1ログインも同じく
-- [作問] ベンチマークは出来ればこれを検証したい
CREATE TABLE login_sessions (
  id varchar(36) PRIMARY KEY,
  user_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  token_hash text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  invalidated_at timestamptz,
  reason text
);

CREATE TABLE administrator_sessions (
  id varchar(36) PRIMARY KEY,
  administrator_id varchar(36) NOT NULL REFERENCES administrators(id) ON DELETE RESTRICT,
  token_hash text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  invalidated_at timestamptz
);

CREATE TABLE friendships (
  id varchar(36) PRIMARY KEY,
  user_low_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  user_high_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  source_request_id varchar(36),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(user_low_id,user_high_id)
);

CREATE TABLE follows (
  id varchar(36) PRIMARY KEY,
  follower_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  followee_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(follower_id,followee_id)
);

CREATE TABLE block_relations (
  id varchar(36) PRIMARY KEY,
  blocker_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  blocked_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(blocker_id,blocked_id)
);

-- [作問] ある論理日付での投稿となる期間を表すWindow
CREATE TABLE posting_windows (
  id varchar(36) PRIMARY KEY,
  region text NOT NULL,
  local_date date NOT NULL,
  starts_at timestamptz NOT NULL,
  on_time_deadline timestamptz NOT NULL,
  ended_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(region,local_date)
);
CREATE UNIQUE INDEX posting_windows_one_active_region ON posting_windows(region) WHERE ended_at IS NULL;

CREATE TABLE posts (
  id varchar(36) PRIMARY KEY,
  author_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  posting_window_id varchar(36) NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,
  image_id varchar(36) NOT NULL,
  caption text NOT NULL DEFAULT '',
  visibility text NOT NULL,
  location_kind text NOT NULL,
  latitude numeric,
  longitude numeric,
  locality text,
  posted_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  deleted_at timestamptz
);
CREATE INDEX posts_author_id ON posts(author_id);
CREATE INDEX posts_posting_window_id ON posts(posting_window_id);

CREATE TABLE friend_requests (
  id varchar(36) PRIMARY KEY,
  requester_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  recipient_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  status text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE realmojis (
  id varchar(36) PRIMARY KEY,
  post_id varchar(36) NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
  reactor_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  image_id varchar(36) NOT NULL,
  reaction_type text NOT NULL,
  created_at timestamptz NOT NULL,
  replaced_at timestamptz,
  deleted_at timestamptz
);
CREATE UNIQUE INDEX realmojis_one_active ON realmojis(post_id,reactor_id) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE INDEX realmojis_post_id ON realmojis(post_id);

CREATE TABLE comments (
  id varchar(36) PRIMARY KEY,
  post_id varchar(36) NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
  author_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  body text NOT NULL,
  created_at timestamptz NOT NULL,
  deleted_at timestamptz
);
CREATE INDEX comments_post_id ON comments(post_id);

CREATE TABLE notifications (
  id varchar(36) PRIMARY KEY,
  user_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  posting_window_id varchar(36) NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL,
  UNIQUE(user_id,posting_window_id)
);

CREATE TABLE ip_bans (
  ip text PRIMARY KEY,
  banned_until timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Derived text only. Raw names are checked on every search so old binaries,
-- imports and initialization cannot make a stale index hide a matching user.
CREATE TABLE IF NOT EXISTS astra_user_search (
    user_id text PRIMARY KEY,
    source_username text NOT NULL,
    source_display_name text NOT NULL,
    normalized_username text COLLATE "C" NOT NULL,
    normalized_display_name text COLLATE "C" NOT NULL
);
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON astra_user_search TO isureal;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS astra_search_dirty (user_id text PRIMARY KEY);

-- One immutable recipient snapshot is published with its posting window.
-- Readers retain support for the original notifications table.
CREATE TABLE IF NOT EXISTS astra_notification_batches (
    posting_window_id varchar(36) PRIMARY KEY REFERENCES posting_windows(id) ON DELETE RESTRICT,
    user_ids text[] NOT NULL,
    notification_ids text[] NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (cardinality(user_ids)=cardinality(notification_ids))
);
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
        GRANT SELECT, INSERT, TRUNCATE ON astra_notification_batches TO isureal;
    END IF;
END $$;
