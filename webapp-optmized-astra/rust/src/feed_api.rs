use std::sync::Arc;

use axum::Extension;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

use crate::api_error::{Error, new_api_error};
use crate::bind::{Bind, Bindable, Binder};
use crate::db::args;
use crate::post::{POST_COLUMNS, PostWithAuthor};
use crate::post_api::{CreatePostResponse, new_post_response};
use crate::response::json;
use crate::server::HandlerContext;
use crate::session::Session;
use crate::user::user_columns_as;

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct GetFeedRequest {
    #[serde(skip)]
    pub feed: String,
    #[serde(skip)]
    pub before_posted_at: Option<DateTime<Utc>>,
    #[serde(skip)]
    pub before_id: String,
}

impl Bindable for GetFeedRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("feed", &mut self.feed)?;
        b.query("beforePostedAt", &mut self.before_posted_at)?;
        b.query("beforeId", &mut self.before_id)
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct GetFeedResponse {
    pub items: Vec<CreatePostResponse>,
    pub has_more: bool,
}

pub async fn get_feed_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<GetFeedRequest>,
) -> Result<Response, Error> {
    match input.feed.as_str() {
        "friends" | "friends-of-friends" | "public" | "following" => {}
        _ => return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post")),
    }
    if input.before_posted_at.is_none() != input.before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    // Filter permissions before LIMIT so hidden posts cannot consume page slots.
    // A single fresh snapshot supplies the viewer, relationships and post gate.
    let mut conditions = String::from(
        r#"
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
    "#,
    );
    conditions.push_str(match input.feed.as_str() {
        "public" => " AND p.visibility = 'public'",
        "friends" => " AND p.author_id IN (SELECT id FROM friends) \
            AND p.visibility IN ('public', 'friends', 'friends_of_friends')",
        "friends-of-friends" => " AND p.author_id NOT IN (SELECT id FROM friends) \
            AND p.author_id IN (SELECT id FROM friends_of_friends) \
            AND p.visibility IN ('public', 'friends_of_friends')",
        "following" => " AND EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = $1 AND f.followee_id = p.author_id) \
            AND (p.visibility = 'public' \
              OR (p.visibility IN ('friends', 'friends_of_friends') AND p.author_id IN (SELECT id FROM friends)) \
              OR (p.visibility = 'friends_of_friends' AND p.author_id IN (SELECT id FROM friends_of_friends)))",
        _ => unreachable!("validated feed type"),
    });
    let args = if let Some(before) = input.before_posted_at {
        conditions.push_str(" AND (p.posted_at, p.id) < ($2, $3)");
        args![&session.user_id, before, &input.before_id]
    } else {
        args![&session.user_id]
    };
    conditions.push_str(" ORDER BY p.posted_at DESC, p.id DESC LIMIT 51");
    // The global top 51 must be within each active window's top 51. The
    // lateral limit keeps the plan on the window index even with few new posts,
    // rather than scanning old public history to try to fill the page.
    let query = format!(
        r#"
        {VIEWER_RELATIONS}
        SELECT candidate.* FROM posting_windows w
        CROSS JOIN LATERAL (
          SELECT {POST_COLUMNS}, {}
          FROM posts p JOIN users u ON u.id = p.author_id AND u.status = 'active'
          WHERE p.posting_window_id = w.id AND p.deleted_at IS NULL {conditions}
        ) candidate
        WHERE w.ended_at IS NULL
        ORDER BY candidate.posted_at DESC, candidate.id DESC LIMIT 51
    "#,
        user_columns_as("u", "author")
    );
    let mut posts: Vec<PostWithAuthor> = sqlx::query_as_with(&query, args).fetch_all(&h.db).await?;
    let has_more = posts.len() > 50;
    let mut items = Vec::with_capacity(posts.len().min(50));
    for post in posts.iter_mut().take(50) {
        post.author.set_image_key()?;
        items.push(new_post_response(&post.post, &post.author));
    }
    json(StatusCode::OK, GetFeedResponse { items, has_more })
}

const VIEWER_RELATIONS: &str = r#"
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
"#;
