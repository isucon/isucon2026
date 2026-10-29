//! Evaluate one post's current permissions without loading whole friend graphs.
use std::sync::LazyLock;

use sqlx::{PgConnection, PgPool};

use crate::api_error::Error;
use crate::post::{POST_COLUMNS, PostWithAuthor};
use crate::user::user_columns_as;

const ACCESS_CTES: &str = r#"
WITH viewer AS (
  SELECT id, region FROM users WHERE id = $1 AND status = 'active'
), target AS MATERIALIZED (
  SELECT p.id, p.author_id, p.visibility, w.ended_at
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users author ON author.id = p.author_id AND author.status = 'active'
  WHERE p.id = $2 AND p.deleted_at IS NULL
), friends AS (
  SELECT user_high_id AS id FROM friendships WHERE user_low_id = $1
  UNION
  SELECT user_low_id AS id FROM friendships WHERE user_high_id = $1
), facts AS (
  SELECT p.*, p.author_id = v.id AS own,
    EXISTS (
      SELECT 1 FROM posts own_post
      JOIN posting_windows posted ON posted.id = own_post.posting_window_id
      JOIN posting_windows current ON current.region = v.region
        AND current.ended_at IS NULL AND current.local_date = posted.local_date
      WHERE own_post.author_id = v.id AND own_post.deleted_at IS NULL
    ) AS has_posted,
    EXISTS (
      SELECT 1 FROM block_relations b
      WHERE (b.blocker_id = v.id AND b.blocked_id = p.author_id)
         OR (b.blocker_id = p.author_id AND b.blocked_id = v.id)
    ) AS blocked,
    p.author_id IN (SELECT id FROM friends) AS direct_friend,
    EXISTS (
      SELECT 1 FROM friendships edge
      WHERE (edge.user_low_id = p.author_id AND edge.user_high_id IN (SELECT id FROM friends))
         OR (edge.user_high_id = p.author_id AND edge.user_low_id IN (SELECT id FROM friends))
    ) AS two_hop
  FROM target p CROSS JOIN viewer v
), permission AS (
  SELECT *, own OR (
    ended_at IS NULL AND has_posted AND NOT blocked AND
    CASE visibility
      WHEN 'public' THEN true
      WHEN 'friends' THEN direct_friend
      WHEN 'friends_of_friends' THEN direct_friend OR two_hop
      ELSE false
    END
  ) AS can_view
  FROM facts
)
"#;

static VIEW_QUERY: LazyLock<String> = LazyLock::new(|| {
    format!("{ACCESS_CTES} SELECT EXISTS(SELECT 1 FROM permission WHERE can_view)")
});

static COMMENT_QUERY: LazyLock<String> = LazyLock::new(|| {
    format!(
        "{ACCESS_CTES} SELECT EXISTS(SELECT 1 FROM permission WHERE can_view \
        AND ended_at IS NULL AND ((own AND has_posted) \
        OR (NOT own AND (visibility = 'public' OR direct_friend))))"
    )
});

static VISIBLE_POST_QUERY: LazyLock<String> = LazyLock::new(|| {
    format!(
        r#"{ACCESS_CTES}
      SELECT {POST_COLUMNS}, {}
      FROM permission access
      JOIN posts p ON p.id = access.id
      JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id
      WHERE access.can_view
    "#,
        user_columns_as("u", "author")
    )
});

pub async fn can_view(
    db: &mut PgConnection,
    viewer_id: &str,
    post_id: &str,
) -> Result<bool, Error> {
    Ok(sqlx::query_scalar(&VIEW_QUERY)
        .bind(viewer_id)
        .bind(post_id)
        .fetch_one(db)
        .await?)
}

pub async fn can_comment(
    db: &mut PgConnection,
    viewer_id: &str,
    post_id: &str,
) -> Result<bool, Error> {
    Ok(sqlx::query_scalar(&COMMENT_QUERY)
        .bind(viewer_id)
        .bind(post_id)
        .fetch_one(db)
        .await?)
}

pub async fn visible_post(
    db: &PgPool,
    viewer_id: &str,
    post_id: &str,
) -> Result<PostWithAuthor, Error> {
    let mut post: PostWithAuthor = sqlx::query_as(&VISIBLE_POST_QUERY)
        .bind(viewer_id)
        .bind(post_id)
        .fetch_one(db)
        .await?;
    post.author.set_image_key()?;
    Ok(post)
}
