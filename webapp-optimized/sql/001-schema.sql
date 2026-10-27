DROP SEQUENCE IF EXISTS change_seq;
DROP TABLE IF EXISTS
  ip_bans,
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
  image_objects CASCADE;

-- [作問] 画像情報
-- [作問] usernameも当然UniqueだがIndex貼っていないので気を付けること
-- [作問] ログインにはsecretKeyを使う。secret_key_hashはサーバーで生成されたsecretKeyをSHA256ハッシュしたもの。

-- 2026-09-01: 画像をDBにおかないと、別のサーバーからデータを返却したときに404になってしまう。ファイル配置に変更して障害になってしまったので注意。ここは決して変更しないこと。
CREATE TABLE image_objects (
  id varchar(36) NOT NULL,
  owner_user_id varchar(36) NOT NULL,
  data bytea NOT NULL,
  -- like: image/jpeg, image/png
  media_type text NOT NULL,
  content_version uuid NOT NULL DEFAULT gen_random_uuid(),
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
CREATE TABLE image_objects_202610 PARTITION OF image_objects FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');
CREATE TABLE image_objects_202611 PARTITION OF image_objects FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');
CREATE TABLE image_objects_202612 PARTITION OF image_objects FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE image_objects_202701 PARTITION OF image_objects FOR VALUES FROM ('2027-01-01 00:00:00+00') TO ('2027-02-01 00:00:00+00');
CREATE TABLE image_objects_202702 PARTITION OF image_objects FOR VALUES FROM ('2027-02-01 00:00:00+00') TO ('2027-03-01 00:00:00+00');
CREATE TABLE image_objects_202703 PARTITION OF image_objects FOR VALUES FROM ('2027-03-01 00:00:00+00') TO ('2027-04-01 00:00:00+00');
CREATE TABLE image_objects_202704 PARTITION OF image_objects FOR VALUES FROM ('2027-04-01 00:00:00+00') TO ('2027-05-01 00:00:00+00');
CREATE TABLE image_objects_202705 PARTITION OF image_objects FOR VALUES FROM ('2027-05-01 00:00:00+00') TO ('2027-06-01 00:00:00+00');
CREATE TABLE image_objects_202706 PARTITION OF image_objects FOR VALUES FROM ('2027-06-01 00:00:00+00') TO ('2027-07-01 00:00:00+00');
CREATE TABLE image_objects_default PARTITION OF image_objects DEFAULT;

-- A fresh version survives ID reuse across initialization and is shared by all
-- app VMs. Metadata reads can validate cached bytes without reading the bytea.
CREATE OR REPLACE FUNCTION refresh_image_content_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.content_version := gen_random_uuid();
  RETURN NEW;
END;
$$;
CREATE TRIGGER image_content_changed BEFORE UPDATE ON image_objects
FOR EACH ROW EXECUTE FUNCTION refresh_image_content_version();

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

CREATE TABLE comments (
  id varchar(36) PRIMARY KEY,
  post_id varchar(36) NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
  author_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  body text NOT NULL,
  created_at timestamptz NOT NULL,
  deleted_at timestamptz
);

-- Versions of hot state changes pushed between the regional apps.
CREATE SEQUENCE change_seq;

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

-- Access paths only: retain the original constraints and deletion semantics.
CREATE INDEX users_username_id ON users(username, id);
CREATE UNIQUE INDEX users_username_unique ON users(username);
CREATE INDEX users_region_active ON users(region, id) WHERE status = 'active';
CREATE INDEX login_sessions_active_token ON login_sessions(token_hash) INCLUDE (user_id, created_at) WHERE active = true;
CREATE INDEX login_sessions_active_user ON login_sessions(user_id) WHERE active = true;
CREATE INDEX administrator_sessions_active_token ON administrator_sessions(token_hash) WHERE active = true;
CREATE INDEX administrator_sessions_active_user ON administrator_sessions(administrator_id) WHERE active = true;
CREATE INDEX administrators_username ON administrators(username);
CREATE INDEX friendships_low_page ON friendships(user_low_id, updated_at DESC, id DESC) INCLUDE (user_high_id);
CREATE INDEX friendships_high_page ON friendships(user_high_id, updated_at DESC, id DESC) INCLUDE (user_low_id);
CREATE INDEX follows_follower_page ON follows(follower_id, updated_at DESC, id DESC) INCLUDE (followee_id);
CREATE INDEX follows_followee_page ON follows(followee_id, updated_at DESC, id DESC) INCLUDE (follower_id);
CREATE INDEX block_relations_reverse ON block_relations(blocked_id, blocker_id);
CREATE INDEX friend_requests_incoming ON friend_requests(recipient_id, updated_at DESC, id DESC) WHERE status = 'pending';
CREATE INDEX friend_requests_outgoing ON friend_requests(requester_id, updated_at DESC, id DESC) WHERE status = 'pending';
CREATE INDEX friend_requests_pair ON friend_requests(requester_id, recipient_id) WHERE status = 'pending';
CREATE INDEX posts_author_window ON posts(author_id, posting_window_id);
CREATE INDEX posts_author_page ON posts(author_id, posted_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_window_page ON posts(posting_window_id, posted_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_public_page ON posts(posted_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public';
-- These full image-reference indexes also serve FK checks when profile images
-- are deleted. An active-only index cannot check references in deleted rows.
CREATE INDEX users_profile_image ON users(profile_image_id) WHERE profile_image_id IS NOT NULL;
CREATE INDEX posts_image ON posts(image_id, author_id);
CREATE INDEX posting_windows_date ON posting_windows(local_date, id);
CREATE INDEX comments_post_page ON comments(post_id, created_at, id) WHERE deleted_at IS NULL;
CREATE INDEX realmojis_post_page ON realmojis(post_id, created_at DESC, id DESC) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE INDEX realmojis_image ON realmojis(image_id, reactor_id) INCLUDE (post_id, replaced_at, deleted_at);
-- The reset deletes the rows the load wrote by created_at; without these the
-- deletes scan the history tables (about 5 s on the large data set).
CREATE INDEX notifications_created ON notifications(created_at);
-- The hot state reads the notified users of the open windows when it loads.
CREATE INDEX notifications_window ON notifications(posting_window_id);
CREATE INDEX comments_created ON comments(created_at);
CREATE INDEX realmojis_created ON realmojis(created_at);
CREATE INDEX posts_created ON posts(created_at);

-- Database-side transactions for high-latency application connections.
-- All routines are SECURITY INVOKER. Each call is one implicit transaction;
-- errors (including user triggers) roll back the entire operation.
CREATE OR REPLACE FUNCTION opt_fail(status integer, code text) RETURNS void
LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION USING ERRCODE='PZ001', MESSAGE=code, DETAIL=status::text;
END $$;

CREATE OR REPLACE FUNCTION opt_user(u users, check_key boolean DEFAULT true) RETURNS jsonb
LANGUAGE plpgsql AS $$ BEGIN
  IF check_key AND u.secret_key_hash !~ '^[0-9a-fA-F]{64}$' THEN
    RAISE EXCEPTION 'invalid image signing key';
  END IF;
  RETURN jsonb_build_object('id',u.id,'username',u.username,'displayName',u.display_name,
    'secretKeyHash',u.secret_key_hash,'profileImageId',u.profile_image_id,
    'bio',u.bio,'region',u.region,'accountType',u.account_type,'status',u.status);
END $$;

CREATE OR REPLACE FUNCTION opt_post(p posts, w posting_windows) RETURNS jsonb
LANGUAGE sql AS $$ SELECT jsonb_build_object(
  'ID',p.id,'AuthorID',p.author_id,'PostingWindowID',p.posting_window_id,
  'ImageID',p.image_id,'Caption',p.caption,'Visibility',p.visibility,
  'kind',p.location_kind,'latitude',p.latitude,'longitude',p.longitude,'locality',p.locality,
  'PostedAt',p.posted_at,'OnTimeDeadline',w.on_time_deadline,'WindowEndedAt',w.ended_at) $$;

CREATE OR REPLACE FUNCTION opt_request(r friend_requests) RETURNS jsonb
LANGUAGE sql AS $$ SELECT jsonb_build_object('ID',r.id,'RequesterID',r.requester_id,
 'RecipientID',r.recipient_id,'Status',r.status,'CreatedAt',r.created_at,'UpdatedAt',r.updated_at) $$;

CREATE OR REPLACE FUNCTION opt_comment(r comments) RETURNS jsonb
LANGUAGE sql AS $$ SELECT jsonb_build_object('ID',r.id,'PostID',r.post_id,
 'AuthorID',r.author_id,'Body',r.body,'CreatedAt',r.created_at,'DeletedAt',r.deleted_at) $$;

CREATE OR REPLACE FUNCTION opt_reaction(r realmojis) RETURNS jsonb
LANGUAGE sql AS $$ SELECT jsonb_build_object('ID',r.id,'PostID',r.post_id,
 'ReactorID',r.reactor_id,'ImageID',r.image_id,'ReactionType',r.reaction_type,
 'CreatedAt',r.created_at,'ReplacedAt',r.replaced_at,'DeletedAt',r.deleted_at) $$;

-- Locks both users in a fixed order. With share_target, the target is locked
-- FOR SHARE: interactions with one popular author then run concurrently, while
-- relationship changes (FOR NO KEY UPDATE on both) still exclude them.
-- User rows are locked FOR NO KEY UPDATE, which serializes the app's own
-- writes but not foreign key checks (FOR KEY SHARE) of other transactions.
DROP FUNCTION IF EXISTS opt_lock_pair(text, text, boolean);
CREATE OR REPLACE FUNCTION opt_lock_pair(actor text, target text, active_only boolean DEFAULT true, share_target boolean DEFAULT false) RETURNS void
LANGUAGE plpgsql AS $$ DECLARE item text; BEGIN
 IF actor=target OR target='' THEN PERFORM opt_fail(400,'invalid_relationship'); END IF;
 FOREACH item IN ARRAY ARRAY[least(actor COLLATE "C",target COLLATE "C"),greatest(actor COLLATE "C",target COLLATE "C")] LOOP
   IF share_target AND item=target THEN
     PERFORM id FROM users WHERE id=item AND (NOT active_only OR status='active') FOR SHARE;
   ELSE
     PERFORM id FROM users WHERE id=item AND (NOT active_only OR status='active') FOR NO KEY UPDATE;
   END IF;
   IF NOT FOUND THEN PERFORM opt_fail(404,'relationship_not_found'); END IF;
 END LOOP;
END $$;

CREATE OR REPLACE FUNCTION opt_blocked(actor text,target text) RETURNS boolean
LANGUAGE sql STABLE AS $$ SELECT EXISTS(SELECT 1 FROM block_relations
 WHERE (blocker_id=actor AND blocked_id=target) OR (blocker_id=target AND blocked_id=actor)) $$;

CREATE OR REPLACE FUNCTION opt_access(actor text, target_post text, comment_mode boolean DEFAULT false) RETURNS boolean
LANGUAGE plpgsql STABLE AS $$
DECLARE p posts; w posting_windows; v users; author users; posted boolean; friend boolean; fof boolean;
BEGIN
 SELECT * INTO p FROM posts WHERE id=target_post AND deleted_at IS NULL;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT * INTO author FROM users WHERE id=p.author_id AND status='active';
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM opt_user(author);
 SELECT * INTO v FROM users WHERE id=actor AND status='active';
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM opt_user(v);
 SELECT * INTO w FROM posting_windows WHERE id=p.posting_window_id;
 IF actor=p.author_id AND NOT comment_mode THEN RETURN true; END IF;
 IF w.ended_at IS NOT NULL THEN RETURN false; END IF;
 SELECT EXISTS(SELECT 1 FROM posts own JOIN posting_windows pw ON pw.id=own.posting_window_id
  JOIN posting_windows current ON current.region=v.region AND current.ended_at IS NULL AND current.local_date=pw.local_date
  WHERE own.author_id=actor AND own.deleted_at IS NULL) INTO posted;
 IF NOT posted THEN RETURN false; END IF;
 IF actor=p.author_id THEN RETURN true; END IF;
 IF opt_blocked(actor,p.author_id) THEN RETURN false; END IF;
 IF p.visibility='public' THEN RETURN true; END IF;
 SELECT EXISTS(SELECT 1 FROM friendships WHERE
  (user_low_id=actor AND user_high_id=p.author_id) OR (user_high_id=actor AND user_low_id=p.author_id)) INTO friend;
 IF comment_mode THEN RETURN friend AND p.visibility IN ('friends','friends_of_friends'); END IF;
 IF p.visibility='friends' THEN RETURN friend; END IF;
 IF p.visibility<>'friends_of_friends' THEN RETURN false; END IF;
 IF friend THEN RETURN true; END IF;
 SELECT EXISTS(SELECT 1 FROM (
   SELECT user_high_id AS id FROM friendships WHERE user_low_id=actor
   UNION ALL SELECT user_low_id FROM friendships WHERE user_high_id=actor
 ) f JOIN friendships other ON
  (other.user_low_id=f.id AND other.user_high_id=p.author_id) OR
  (other.user_high_id=f.id AND other.user_low_id=p.author_id)) INTO fof;
 RETURN fof;
END $$;

CREATE OR REPLACE FUNCTION optimized_write(action text, actor text, a jsonb, image bytea) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE u users; target_user users; p posts; w posting_windows; req friend_requests;
 c comments; r realmojis; target text; old_image text; newid text; imageid text;
 n bigint; active_count bigint; deleted_count bigint; at_time timestamptz; result jsonb;
BEGIN
 IF action='register' THEN
  -- The unique index decides concurrent registrations without a table lock.
  INSERT INTO users(id,username,secret_key_hash,display_name,region)
   VALUES(a->>'id',a->>'username',a->>'hash',a->>'displayName',a->>'region')
   ON CONFLICT (username) DO NOTHING RETURNING * INTO u;
  IF NOT FOUND THEN PERFORM opt_fail(409,'username_taken'); END IF;
  RETURN jsonb_build_object('user',opt_user(u));
 ELSIF action='login' THEN
  SELECT * INTO u FROM users WHERE username=a->>'username' FOR NO KEY UPDATE;
  IF NOT FOUND OR u.status<>'active' OR u.secret_key_hash<>a->>'hash' THEN PERFORM opt_fail(401,'invalid_credentials'); END IF;
  UPDATE login_sessions SET active=false,invalidated_at=clock_timestamp(),reason='replaced' WHERE user_id=u.id AND active=true;
  INSERT INTO login_sessions(id,user_id,token_hash) VALUES(a->>'id',u.id,a->>'tokenHash');
  RETURN jsonb_build_object('user',opt_user(u,false));
 ELSIF action='logout' THEN
  PERFORM id FROM users WHERE id=actor FOR NO KEY UPDATE;
  UPDATE login_sessions SET active=false,invalidated_at=clock_timestamp(),reason='logout'
   WHERE user_id=actor AND active=true AND token_hash=a->>'tokenHash';
  RETURN '{}'::jsonb;
 ELSIF action='profile_update' THEN
  UPDATE users SET display_name=coalesce(a->>'displayName',display_name),bio=coalesce(a->>'bio',bio),
   region=coalesce(a->>'region',region),updated_at=clock_timestamp()
   WHERE id=actor AND status='active' RETURNING * INTO u;
  IF NOT FOUND THEN PERFORM opt_fail(404,'account_not_found'); END IF;
  RETURN jsonb_build_object('user',opt_user(u));
 ELSIF action='account_delete' THEN
  SELECT * INTO u FROM users WHERE id=actor AND status='active' FOR NO KEY UPDATE;
  IF NOT FOUND THEN PERFORM opt_fail(404,'account_not_found'); END IF;
  UPDATE users SET status='deleted',deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=actor;
  UPDATE login_sessions SET active=false,invalidated_at=clock_timestamp(),reason='account_deletion' WHERE user_id=actor AND active=true;
  RETURN '{}'::jsonb;
 ELSIF action IN ('profile_image_put','profile_image_delete') THEN
  SELECT * INTO u FROM users WHERE id=actor FOR NO KEY UPDATE;
  IF NOT FOUND THEN PERFORM opt_fail(404,'account_not_found'); END IF;
  old_image:=u.profile_image_id;
  IF action='profile_image_delete' AND old_image IS NULL THEN PERFORM opt_fail(404,'account_not_found'); END IF;
  imageid:=NULL;
  IF action='profile_image_put' THEN
   imageid:=coalesce(a->>'imageId',gen_random_uuid()::text);
   INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES(imageid,actor,image,'image/jpeg');
  END IF;
  UPDATE users SET profile_image_id=imageid,updated_at=clock_timestamp() WHERE id=actor;
  IF old_image IS NOT NULL THEN DELETE FROM image_objects WHERE id=old_image; END IF;
  RETURN '{}'::jsonb;
 ELSIF action IN ('post_create','post_update','post_delete') THEN
  SELECT * INTO u FROM users WHERE id=actor FOR NO KEY UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'user not found'; END IF;
  IF action='post_create' THEN
   IF u.status<>'active' THEN RAISE EXCEPTION 'user not active'; END IF;
   result:=opt_user(u);
   SELECT * INTO w FROM posting_windows WHERE region=u.region AND ended_at IS NULL FOR SHARE;
   IF NOT FOUND THEN
    -- The open window may have just been replaced: the locked row no longer
    -- matches and the new one is not in this statement's snapshot. A new
    -- statement sees the new window.
    SELECT * INTO w FROM posting_windows WHERE region=u.region AND ended_at IS NULL FOR SHARE;
   END IF;
   IF NOT FOUND THEN PERFORM opt_fail(409,'post_slot_unavailable'); END IF;
   SELECT count(*) FILTER(WHERE pp.deleted_at IS NULL),count(*) FILTER(WHERE pp.deleted_at IS NOT NULL)
    INTO active_count,deleted_count FROM posts pp JOIN posting_windows pw ON pw.id=pp.posting_window_id
    WHERE pp.author_id=actor AND pw.local_date=w.local_date;
   at_time:=clock_timestamp();
   IF active_count<>0 OR deleted_count>=2 OR at_time<w.starts_at THEN PERFORM opt_fail(409,'post_slot_unavailable'); END IF;
   imageid:=coalesce(a->>'imageId',gen_random_uuid()::text); newid:=gen_random_uuid()::text;
   INSERT INTO image_objects(id,owner_user_id,data,media_type,created_at) VALUES(imageid,actor,image,'image/jpeg',at_time);
   INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,location_kind,latitude,longitude,locality,posted_at)
    VALUES(newid,actor,w.id,imageid,a->>'caption',a->>'visibility',a->>'kind',
     (a->>'latitude')::numeric,(a->>'longitude')::numeric,a->>'locality',at_time) RETURNING * INTO p;
  ELSE
   SELECT * INTO p FROM posts WHERE id=a->>'id' AND deleted_at IS NULL;
   IF NOT FOUND THEN PERFORM opt_fail(404,'post_not_found'); END IF;
   IF p.author_id<>actor THEN PERFORM opt_fail(403,'post_not_editable'); END IF;
   SELECT * INTO w FROM posting_windows WHERE id=p.posting_window_id;
   IF action='post_delete' THEN
    SELECT count(*) INTO deleted_count FROM posts pp JOIN posting_windows pw ON pw.id=pp.posting_window_id
     WHERE pp.author_id=actor AND pw.local_date=w.local_date AND pp.deleted_at IS NOT NULL;
    IF deleted_count>=2 THEN PERFORM opt_fail(409,'post_slot_unavailable'); END IF;
    UPDATE posts SET deleted_at=clock_timestamp() WHERE id=p.id;
    RETURN '{}'::jsonb;
   END IF;
   IF w.ended_at IS NOT NULL THEN PERFORM opt_fail(403,'post_not_editable'); END IF;
   UPDATE posts SET caption=coalesce(a->>'caption',caption),visibility=coalesce(a->>'visibility',visibility)
    WHERE id=p.id RETURNING * INTO p;
   IF u.status<>'active' THEN RAISE EXCEPTION 'user not active'; END IF;
   result:=opt_user(u);
  END IF;
  RETURN jsonb_build_object('user',result,'post',opt_post(p,w));
 ELSIF action IN ('comment_create','reaction_create') THEN
  SELECT * INTO p FROM posts WHERE id=a->>'postId' AND deleted_at IS NULL;
  IF NOT FOUND THEN PERFORM opt_fail(403,'interaction_forbidden'); END IF;
  IF actor=p.author_id THEN
   IF action='reaction_create' THEN PERFORM opt_fail(403,'interaction_forbidden'); END IF;
   PERFORM id FROM users WHERE id=actor FOR NO KEY UPDATE;
  ELSE
   PERFORM opt_lock_pair(actor,p.author_id,action='reaction_create',true);
  END IF;
  IF NOT opt_access(actor,p.id,action='comment_create') THEN PERFORM opt_fail(403,'interaction_forbidden'); END IF;
  SELECT * INTO u FROM users WHERE id=actor AND status='active';
  result:=opt_user(u);
  newid:=gen_random_uuid()::text;
  IF action='comment_create' THEN
   INSERT INTO comments(id,post_id,author_id,body,created_at) VALUES(newid,p.id,actor,a->>'body',clock_timestamp()) RETURNING * INTO c;
   RETURN jsonb_build_object('user',result,'comment',opt_comment(c));
  END IF;
  UPDATE realmojis SET replaced_at=clock_timestamp() WHERE post_id=p.id AND reactor_id=actor AND replaced_at IS NULL AND deleted_at IS NULL;
  imageid:=coalesce(a->>'imageId',gen_random_uuid()::text);
  INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES(imageid,actor,image,'image/jpeg');
  INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at)
   VALUES(newid,p.id,actor,imageid,a->>'reactionType',clock_timestamp()) RETURNING * INTO r;
  RETURN jsonb_build_object('user',result,'reaction',opt_reaction(r));
 ELSIF action IN ('comment_delete','reaction_delete') THEN
  PERFORM id FROM users WHERE id=actor FOR NO KEY UPDATE;
  IF action='comment_delete' THEN
   SELECT cc.* INTO c FROM comments cc JOIN posts pp ON pp.id=cc.post_id WHERE cc.id=a->>'id' AND cc.deleted_at IS NULL FOR UPDATE OF cc;
   IF NOT FOUND THEN PERFORM opt_fail(404,'interaction_not_found'); END IF;
   SELECT author_id INTO target FROM posts WHERE id=c.post_id;
   IF actor<>c.author_id AND actor<>target THEN PERFORM opt_fail(403,'interaction_forbidden'); END IF;
   UPDATE comments SET deleted_at=clock_timestamp() WHERE id=c.id;
  ELSE
   SELECT rr.* INTO r FROM realmojis rr JOIN posts pp ON pp.id=rr.post_id WHERE rr.id=a->>'id' AND rr.deleted_at IS NULL FOR UPDATE OF rr;
   IF NOT FOUND THEN PERFORM opt_fail(404,'interaction_not_found'); END IF;
   SELECT author_id INTO target FROM posts WHERE id=r.post_id;
   IF actor<>r.reactor_id AND actor<>target THEN PERFORM opt_fail(403,'interaction_forbidden'); END IF;
   UPDATE realmojis SET deleted_at=clock_timestamp() WHERE id=r.id;
  END IF;
  RETURN '{}'::jsonb;
 ELSIF action='friend_transition' THEN
  SELECT * INTO req FROM friend_requests WHERE id=a->>'id';
  IF NOT FOUND THEN PERFORM opt_fail(404,'relationship_not_found'); END IF;
  IF (a->>'status'='cancelled' AND actor<>req.requester_id) OR (a->>'status'<>'cancelled' AND actor<>req.recipient_id) THEN
   PERFORM opt_fail(403,'relationship_action_forbidden');
  END IF;
  PERFORM opt_lock_pair(req.requester_id,req.recipient_id);
  SELECT * INTO req FROM friend_requests WHERE id=a->>'id';
  IF req.status<>'pending' THEN PERFORM opt_fail(403,'relationship_action_forbidden'); END IF;
  IF a->>'status'='accepted' THEN
   IF opt_blocked(req.requester_id,req.recipient_id) THEN PERFORM opt_fail(409,'relationship_conflict'); END IF;
   INSERT INTO friendships(id,user_low_id,user_high_id,source_request_id)
    VALUES(gen_random_uuid()::text,least(req.requester_id COLLATE "C",req.recipient_id COLLATE "C"),
     greatest(req.requester_id COLLATE "C",req.recipient_id COLLATE "C"),req.id);
  END IF;
  UPDATE friend_requests SET status=a->>'status',updated_at=clock_timestamp() WHERE id=req.id RETURNING * INTO req;
  RETURN jsonb_build_object('request',opt_request(req));
 ELSE
  target:=a->>'target';
  PERFORM opt_lock_pair(actor,target);
  IF action='friend_create' THEN
   IF opt_blocked(actor,target) OR EXISTS(SELECT 1 FROM friendships WHERE
     user_low_id=(least(actor COLLATE "C",target COLLATE "C") COLLATE "default") AND user_high_id=(greatest(actor COLLATE "C",target COLLATE "C") COLLATE "default")) THEN
    PERFORM opt_fail(409,'relationship_conflict');
   END IF;
   SELECT * INTO req FROM friend_requests WHERE status='pending' AND
    ((requester_id=actor AND recipient_id=target) OR (requester_id=target AND recipient_id=actor));
   IF FOUND THEN
    IF req.requester_id=actor THEN PERFORM opt_fail(409,'relationship_conflict'); END IF;
    UPDATE friend_requests SET status='accepted',updated_at=clock_timestamp() WHERE id=req.id RETURNING * INTO req;
    INSERT INTO friendships(id,user_low_id,user_high_id,source_request_id)
     VALUES(gen_random_uuid()::text,least(actor COLLATE "C",target COLLATE "C"),greatest(actor COLLATE "C",target COLLATE "C"),req.id);
   ELSE
    INSERT INTO friend_requests(id,requester_id,recipient_id,status,created_at,updated_at)
     VALUES(gen_random_uuid()::text,actor,target,'pending',clock_timestamp(),clock_timestamp()) RETURNING * INTO req;
   END IF;
   RETURN jsonb_build_object('request',opt_request(req));
  ELSIF action='follow' THEN
   SELECT * INTO target_user FROM users WHERE id=target;
   PERFORM opt_user(target_user);
   IF target_user.account_type<>'certified_public' OR opt_blocked(actor,target) OR
    EXISTS(SELECT 1 FROM follows WHERE follower_id=actor AND followee_id=target) THEN PERFORM opt_fail(409,'relationship_conflict'); END IF;
   INSERT INTO follows(id,follower_id,followee_id) VALUES(gen_random_uuid()::text,actor,target);
  ELSIF action='block' THEN
   IF EXISTS(SELECT 1 FROM block_relations WHERE blocker_id=actor AND blocked_id=target) THEN PERFORM opt_fail(409,'relationship_conflict'); END IF;
   INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES(gen_random_uuid()::text,actor,target);
   DELETE FROM friendships WHERE user_low_id=(least(actor COLLATE "C",target COLLATE "C") COLLATE "default") AND user_high_id=(greatest(actor COLLATE "C",target COLLATE "C") COLLATE "default");
   DELETE FROM follows WHERE (follower_id=actor AND followee_id=target) OR (follower_id=target AND followee_id=actor);
   UPDATE friend_requests SET status='cancelled',updated_at=clock_timestamp() WHERE status='pending' AND
    ((requester_id=actor AND recipient_id=target) OR (requester_id=target AND recipient_id=actor));
  ELSIF action='unfollow' THEN
   DELETE FROM follows WHERE follower_id=actor AND followee_id=target;
   IF NOT FOUND THEN PERFORM opt_fail(409,'relationship_conflict'); END IF;
  ELSIF action='unblock' THEN
   DELETE FROM block_relations WHERE blocker_id=actor AND blocked_id=target;
   IF NOT FOUND THEN PERFORM opt_fail(403,'relationship_action_forbidden'); END IF;
  ELSIF action='friend_delete' THEN
   DELETE FROM friendships WHERE user_low_id=(least(actor COLLATE "C",target COLLATE "C") COLLATE "default") AND user_high_id=(greatest(actor COLLATE "C",target COLLATE "C") COLLATE "default");
   IF NOT FOUND THEN PERFORM opt_fail(409,'relationship_conflict'); END IF;
  ELSE RAISE EXCEPTION 'unknown optimized operation: %',action;
  END IF;
  RETURN '{}'::jsonb;
 END IF;
END $$;

-- Used as the first statement of an extended-protocol batch. The actor is
-- transaction-local and disappears when the batch commits or rolls back.
DROP FUNCTION IF EXISTS optimized_auth(text,text);
DROP FUNCTION IF EXISTS optimized_auth(text,text,boolean);
CREATE FUNCTION optimized_auth(token text, required_region text, include_user boolean DEFAULT false)
RETURNS TABLE(user_id text, region text, created_at timestamptz, user_data jsonb)
LANGUAGE plpgsql AS $$
BEGIN
 SELECT s.user_id,u.region,s.created_at,CASE WHEN include_user THEN opt_user(u,false) END INTO user_id,region,created_at,user_data
 FROM login_sessions s JOIN users u ON u.id=s.user_id
 WHERE s.token_hash=token AND s.active=true AND u.status='active';
 IF NOT FOUND THEN PERFORM opt_fail(401,'invalid_session'); END IF;
 IF required_region<>'' AND region<>required_region THEN PERFORM opt_fail(403,'region_mismatch'); END IF;
 PERFORM set_config('isureal.actor',user_id,true);
 RETURN NEXT;
END $$;
