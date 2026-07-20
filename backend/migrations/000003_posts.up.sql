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
