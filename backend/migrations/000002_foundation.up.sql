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
  password_hash text NOT NULL,
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
