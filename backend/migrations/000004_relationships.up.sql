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
