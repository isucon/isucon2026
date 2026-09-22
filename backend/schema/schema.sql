-- ISUReal application schema. Applied as a single script.

CREATE TABLE image_objects (
  id uuid PRIMARY KEY,
  owner_user_id uuid NOT NULL,
  sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  object_key text NOT NULL UNIQUE,
  byte_size bigint NOT NULL CHECK (byte_size > 0),
  media_type text NOT NULL CHECK (media_type = 'image/jpeg'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE users (
  id uuid PRIMARY KEY,
  username varchar(64) NOT NULL CHECK (username ~ '^[A-Za-z0-9_.-]{1,64}$'),
  username_normalized varchar(64) NOT NULL UNIQUE,
  secret_key_hash text NOT NULL,
  display_name text NOT NULL,
  display_name_search text NOT NULL,
  bio text NOT NULL DEFAULT '',
  profile_image_id uuid REFERENCES image_objects(id) ON DELETE RESTRICT,
  region text NOT NULL CHECK (region IN ('america','europe','asia')),
  pending_region text CHECK (pending_region IN ('america','europe','asia') AND pending_region <> region),
  account_type text NOT NULL DEFAULT 'regular' CHECK (account_type IN ('regular','certified_public')),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','deletion_pending','purging')),
  deletion_requested_at timestamptz,
  purge_due_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK ((status = 'active' AND deletion_requested_at IS NULL AND purge_due_at IS NULL) OR
         (status <> 'active' AND deletion_requested_at IS NOT NULL AND purge_due_at IS NOT NULL))
);
ALTER TABLE image_objects ADD CONSTRAINT image_objects_owner_fk FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE RESTRICT;

CREATE TABLE administrators (
  id uuid PRIMARY KEY,
  login_name_normalized varchar(64) NOT NULL UNIQUE,
  password_hash text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE login_sessions (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  token_hash char(64) NOT NULL UNIQUE,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  invalidated_at timestamptz,
  reason text,
  CHECK ((active AND invalidated_at IS NULL) OR (NOT active AND invalidated_at IS NOT NULL))
);
CREATE UNIQUE INDEX login_sessions_one_active ON login_sessions(user_id) WHERE active;

CREATE TABLE administrator_sessions (
  id uuid PRIMARY KEY,
  administrator_id uuid NOT NULL REFERENCES administrators(id) ON DELETE RESTRICT,
  token_hash char(64) NOT NULL UNIQUE,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  invalidated_at timestamptz
);
CREATE UNIQUE INDEX administrator_sessions_one_active ON administrator_sessions(administrator_id) WHERE active;

CREATE TABLE browser_push_subscriptions (
  id uuid PRIMARY KEY,
  session_id uuid NOT NULL REFERENCES login_sessions(id) ON DELETE RESTRICT,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  endpoint text NOT NULL,
  p256dh text NOT NULL,
  auth_secret text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  invalidated_at timestamptz,
  reason text
);
CREATE UNIQUE INDEX push_subscriptions_one_active_session ON browser_push_subscriptions(session_id) WHERE active;

CREATE TABLE friendships (
  id uuid PRIMARY KEY,
  user_low_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  user_high_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  source_request_id uuid,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(user_low_id,user_high_id), CHECK (user_low_id < user_high_id)
);
CREATE TABLE follows (
  id uuid PRIMARY KEY,
  follower_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  followee_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(follower_id,followee_id), CHECK (follower_id <> followee_id)
);
CREATE TABLE block_relations (
  id uuid PRIMARY KEY,
  blocker_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  blocked_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(blocker_id,blocked_id), CHECK (blocker_id <> blocked_id)
);

CREATE TABLE idempotency_records (
  id uuid PRIMARY KEY,
  caller_scope_type text NOT NULL CHECK (caller_scope_type IN ('user','admin','user_login','admin_login','registration')),
  caller_scope_key text NOT NULL,
  operation text NOT NULL,
  key uuid NOT NULL,
  generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
  request_hash char(64) NOT NULL,
  status text NOT NULL CHECK (status IN ('processing','retryable','completed')),
  response_status integer,
  response_body jsonb,
  finalized_at timestamptz,
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(caller_scope_type,caller_scope_key,operation,key,generation),
  CHECK ((status='completed' AND response_status IS NOT NULL AND finalized_at IS NOT NULL AND expires_at IS NOT NULL) OR status<>'completed')
);
CREATE INDEX idempotency_expiry ON idempotency_records(expires_at) WHERE status='completed';

CREATE TABLE worker_jobs (
  id uuid PRIMARY KEY,
  kind text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','retryable','completed','failed')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  last_error_class text,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX worker_jobs_claim ON worker_jobs(status,next_attempt_at,id) WHERE status IN ('pending','retryable');

CREATE TABLE app_settings (
  key text PRIMARY KEY,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE posting_windows (
  id uuid PRIMARY KEY,
  region text NOT NULL CHECK (region IN ('america','europe','asia')),
  local_date date NOT NULL,
  utc_offset_minutes smallint NOT NULL CHECK (
    (region='america' AND utc_offset_minutes=-300) OR
    (region='europe' AND utc_offset_minutes=60) OR
    (region='asia' AND utc_offset_minutes=540)
  ),
  starts_at timestamptz NOT NULL,
  on_time_deadline timestamptz NOT NULL,
  ended_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(region,local_date),
  CHECK (on_time_deadline=starts_at+interval '2 minutes'),
  CHECK (ended_at IS NULL OR ended_at>starts_at)
);
CREATE UNIQUE INDEX posting_windows_one_active_region ON posting_windows(region) WHERE ended_at IS NULL;
CREATE INDEX posting_windows_region_current ON posting_windows(region,ended_at,starts_at DESC);

CREATE TABLE user_posting_window_states (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  posting_window_id uuid NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,
  active_post_id uuid,
  deletion_count smallint NOT NULL DEFAULT 0 CHECK (deletion_count BETWEEN 0 AND 2),
  posted_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(user_id,posting_window_id),
  CHECK ((active_post_id IS NULL) OR posted_at IS NOT NULL)
);

CREATE TABLE posts (
  id uuid PRIMARY KEY,
  author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  posting_window_id uuid NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,
  image_id uuid NOT NULL REFERENCES image_objects(id) ON DELETE RESTRICT,
  caption text NOT NULL DEFAULT '',
  visibility text NOT NULL CHECK (visibility IN ('friends','friends_of_friends','public')),
  location_kind text NOT NULL CHECK (location_kind IN ('none','exact','approximate')),
  latitude numeric,
  longitude numeric,
  locality text,
  posted_at timestamptz NOT NULL,
  is_on_time boolean NOT NULL,
  late_seconds bigint NOT NULL CHECK (late_seconds>=0),
  deleted_at timestamptz,
  CHECK (
    (location_kind='none' AND latitude IS NULL AND longitude IS NULL AND locality IS NULL) OR
    (location_kind='exact' AND latitude BETWEEN -90 AND 90 AND longitude BETWEEN -180 AND 180 AND locality IS NULL) OR
    (location_kind='approximate' AND latitude IS NULL AND longitude IS NULL AND locality IS NOT NULL AND char_length(locality)>0)
  ),
  CHECK ((is_on_time AND late_seconds=0) OR (NOT is_on_time AND late_seconds>0))
);
ALTER TABLE user_posting_window_states ADD CONSTRAINT active_post_fk FOREIGN KEY(active_post_id) REFERENCES posts(id) ON DELETE RESTRICT;
CREATE INDEX posts_window_feed ON posts(posting_window_id,posted_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_author_history ON posts(author_id,posted_at DESC,id DESC);
CREATE INDEX posts_public_feed ON posts(visibility,posted_at DESC,id DESC) WHERE deleted_at IS NULL;

CREATE TABLE friend_requests (
  id uuid PRIMARY KEY,
  requester_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  recipient_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  user_low_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  user_high_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  status text NOT NULL CHECK(status IN ('pending','accepted','rejected','cancelled','mutualized')),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK(requester_id<>recipient_id AND user_low_id<user_high_id),
  CHECK(user_low_id=LEAST(requester_id,recipient_id) AND user_high_id=GREATEST(requester_id,recipient_id))
);
CREATE UNIQUE INDEX friend_requests_one_pending_pair ON friend_requests(user_low_id,user_high_id) WHERE status='pending';
CREATE INDEX friend_requests_requester_list ON friend_requests(requester_id,updated_at DESC,id DESC) WHERE status='pending';
CREATE INDEX friend_requests_recipient_list ON friend_requests(recipient_id,updated_at DESC,id DESC) WHERE status='pending';
CREATE INDEX friendships_low_list ON friendships(user_low_id,updated_at DESC,id DESC);
CREATE INDEX friendships_high_list ON friendships(user_high_id,updated_at DESC,id DESC);
CREATE INDEX follows_follower_list ON follows(follower_id,updated_at DESC,id DESC);
CREATE INDEX follows_followee_list ON follows(followee_id,updated_at DESC,id DESC);
CREATE INDEX users_search_username ON users(username_normalized,id);
CREATE INDEX users_search_display_name ON users(display_name_search,id);

CREATE TABLE realmojis(
 id uuid PRIMARY KEY,post_id uuid NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,reactor_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,image_id uuid NOT NULL REFERENCES image_objects(id) ON DELETE RESTRICT,reaction_type text NOT NULL CHECK(reaction_type IN('like','smile','surprise','heart','laugh','instant')),created_at timestamptz NOT NULL,replaced_at timestamptz,deleted_at timestamptz
);
CREATE UNIQUE INDEX realmojis_one_active ON realmojis(post_id,reactor_id) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE INDEX realmojis_list ON realmojis(post_id,created_at DESC,id DESC) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE TABLE comments(
 id uuid PRIMARY KEY,post_id uuid NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,body text NOT NULL CHECK(char_length(body) BETWEEN 1 AND 1000),created_at timestamptz NOT NULL,deleted_at timestamptz
);
CREATE INDEX comments_list ON comments(post_id,created_at,id) WHERE deleted_at IS NULL;

CREATE INDEX users_purge_due ON users(purge_due_at,id) WHERE status='deletion_pending';
CREATE INDEX posts_memories_month ON posts(author_id,posted_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX posting_windows_memory_date ON posting_windows(region,local_date,id);
CREATE INDEX image_objects_owner ON image_objects(owner_user_id,id);

CREATE TABLE notification_schedules(region text PRIMARY KEY CHECK(region IN('america','europe','asia')),scheduled_at timestamptz NOT NULL,local_date date NOT NULL,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL);
CREATE INDEX notification_schedules_due ON notification_schedules(scheduled_at,region);
CREATE TABLE notifications(id uuid PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,posting_window_id uuid NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,region text NOT NULL CHECK(region IN('america','europe','asia')),created_at timestamptz NOT NULL,read_at timestamptz,UNIQUE(user_id,posting_window_id));
CREATE INDEX notifications_user_list ON notifications(user_id,created_at DESC,id DESC);
CREATE TABLE push_attempts(id uuid PRIMARY KEY,notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,subscription_id uuid NOT NULL REFERENCES browser_push_subscriptions(id) ON DELETE RESTRICT,status text NOT NULL CHECK(status IN('pending','processing','retryable','delivered','permanent_failure','failed')),attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 4),next_attempt_at timestamptz NOT NULL,last_error_class text,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,UNIQUE(notification_id,subscription_id));
CREATE INDEX push_attempts_claim ON push_attempts(status,next_attempt_at,id) WHERE status IN('pending','processing','retryable');
