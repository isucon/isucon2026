WITH viewer AS (
  SELECT id, region FROM users WHERE id = $1 AND status = 'active'
), friends AS (
  SELECT user_high_id AS id FROM friendships WHERE user_low_id = $1
  UNION
  SELECT user_low_id AS id FROM friendships WHERE user_high_id = $1
), friends_of_friends AS (
  SELECT f.user_high_id AS id FROM friends one JOIN friendships f ON f.user_low_id = one.id
  UNION
  SELECT f.user_low_id AS id FROM friends one JOIN friendships f ON f.user_high_id = one.id
)

        SELECT candidate.* FROM posting_windows w
        CROSS JOIN LATERAL ((
          SELECT p.id, p.author_id, p.posting_window_id, p.image_id, p.caption, p.visibility,
  p.location_kind, p.latitude, p.longitude, p.locality, p.posted_at,
  w.on_time_deadline, w.ended_at AS window_ended_at, u.id AS "author.id",u.username AS "author.username",u.secret_key_hash AS "author.secret_key_hash",u.display_name AS "author.display_name",u.bio AS "author.bio",u.profile_image_id AS "author.profile_image_id",u.region AS "author.region",u.account_type AS "author.account_type",u.status AS "author.status"
          FROM posts p JOIN users u ON u.id = p.author_id AND u.status = 'active'
          WHERE p.posting_window_id = w.id AND p.deleted_at IS NULL
      AND p.author_id <> $1 AND w.ended_at IS NULL
      AND EXISTS (SELECT 1 FROM viewer v WHERE EXISTS (
        SELECT 1 FROM posts own
        JOIN posting_windows posted ON posted.id = own.posting_window_id
        JOIN posting_windows current ON current.region = v.region
          AND current.ended_at IS NULL AND current.local_date = posted.local_date
        WHERE own.author_id = v.id AND own.deleted_at IS NULL
      ))
      AND NOT EXISTS (SELECT 1 FROM block_relations b
        WHERE b.blocker_id = $1 AND b.blocked_id = p.author_id)
      AND NOT EXISTS (SELECT 1 FROM block_relations b
        WHERE b.blocked_id = $1 AND b.blocker_id = p.author_id)
     AND p.author_id NOT IN (SELECT id FROM friends)  AND p.author_id IN (SELECT id FROM friends_of_friends)  AND p.visibility IN ('public', 'friends_of_friends') AND (SELECT count(*) FROM friends_of_friends)<=128 ORDER BY p.posted_at DESC,p.id DESC LIMIT 51) UNION ALL (
          SELECT p.id, p.author_id, p.posting_window_id, p.image_id, p.caption, p.visibility,
  p.location_kind, p.latitude, p.longitude, p.locality, p.posted_at,
  w.on_time_deadline, w.ended_at AS window_ended_at, u.id AS "author.id",u.username AS "author.username",u.secret_key_hash AS "author.secret_key_hash",u.display_name AS "author.display_name",u.bio AS "author.bio",u.profile_image_id AS "author.profile_image_id",u.region AS "author.region",u.account_type AS "author.account_type",u.status AS "author.status"
          FROM posts p JOIN users u ON u.id = p.author_id AND u.status = 'active'
          WHERE p.posting_window_id = w.id AND p.deleted_at IS NULL
      AND p.author_id <> $1 AND w.ended_at IS NULL
      AND EXISTS (SELECT 1 FROM viewer v WHERE EXISTS (
        SELECT 1 FROM posts own
        JOIN posting_windows posted ON posted.id = own.posting_window_id
        JOIN posting_windows current ON current.region = v.region
          AND current.ended_at IS NULL AND current.local_date = posted.local_date
        WHERE own.author_id = v.id AND own.deleted_at IS NULL
      ))
      AND NOT EXISTS (SELECT 1 FROM block_relations b
        WHERE b.blocker_id = $1 AND b.blocked_id = p.author_id)
      AND NOT EXISTS (SELECT 1 FROM block_relations b
        WHERE b.blocked_id = $1 AND b.blocker_id = p.author_id)
     AND p.author_id NOT IN (SELECT id FROM friends)  AND (p.author_id IN (SELECT id FROM friends_of_friends)) IS TRUE  AND p.visibility IN ('public', 'friends_of_friends') AND (SELECT count(*) FROM friends_of_friends)>128 ORDER BY p.posted_at DESC,p.id DESC LIMIT 51)) candidate
        WHERE w.ended_at IS NULL
        ORDER BY candidate.posted_at DESC, candidate.id DESC LIMIT 51
