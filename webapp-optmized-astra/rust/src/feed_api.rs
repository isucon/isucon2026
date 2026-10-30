use std::sync::Arc;

use axum::Extension;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use sqlx::postgres::PgRow;
use sqlx::{FromRow, Row};

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
    pub before_score: Option<i64>,
    #[serde(skip)]
    pub before_posted_at: Option<DateTime<Utc>>,
    #[serde(skip)]
    pub before_id: String,
}

impl Bindable for GetFeedRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("feed", &mut self.feed)?;
        b.query("beforeScore", &mut self.before_score)?;
        b.query("beforePostedAt", &mut self.before_posted_at)?;
        b.query("beforeId", &mut self.before_id)
    }
}

#[derive(Serialize)]
pub struct FeedItemResponse {
    #[serde(flatten)]
    pub create_post_response: CreatePostResponse,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub score: Option<i64>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct GetFeedResponse {
    pub items: Vec<FeedItemResponse>,
    pub has_more: bool,
}

struct RankedPost {
    post: PostWithAuthor,
    score: i64,
}

impl FromRow<'_, PgRow> for RankedPost {
    fn from_row(row: &PgRow) -> sqlx::Result<RankedPost> {
        Ok(RankedPost {
            post: PostWithAuthor::from_row(row)?,
            score: row.try_get("score")?,
        })
    }
}

pub async fn get_feed_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    graph: Option<Extension<Arc<crate::initial_graph::InitialGraph>>>,
    recommendation_reads: Option<Extension<crate::recommendation_reads::RecommendationReads>>,
    Bind(input): Bind<GetFeedRequest>,
) -> Result<Response, Error> {
    match input.feed.as_str() {
        "friends" | "friends-of-friends" | "public" | "following" => {}
        _ => return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post")),
    }
    if input.before_posted_at.is_none() != input.before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let ranked = input.feed == "friends-of-friends";
    if ranked && input.before_score.is_none() != input.before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let ranked_db = recommendation_reads
        .as_ref()
        .map_or(&h.db, |Extension(reads)| &reads.0);
    if ranked
        && let Some(Extension(graph)) = graph
        && let Some(mut posts) = graph
            .recommended(ranked_db, &session.user_id, &input)
            .await?
    {
        let has_more = posts.len() > 50;
        let mut items = Vec::with_capacity(posts.len().min(50));
        for (post, score) in posts.iter_mut().take(50) {
            post.author.set_image_key()?;
            items.push(FeedItemResponse {
                create_post_response: new_post_response(&post.post, &post.author),
                score: Some(*score),
            });
        }
        return json(StatusCode::OK, GetFeedResponse { items, has_more });
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
    let args = if ranked {
        // The recommended order is not by time, so every visible candidate is
        // scored and the cursor applies to (score, postedAt, id) below.
        args![
            &session.user_id,
            input.before_score,
            input.before_posted_at,
            &input.before_id
        ]
    } else if let Some(before) = input.before_posted_at {
        conditions.push_str(" AND (p.posted_at, p.id) < ($2, $3)");
        args![&session.user_id, before, &input.before_id]
    } else {
        args![&session.user_id]
    };
    let order = if ranked {
        ""
    } else {
        " ORDER BY p.posted_at DESC, p.id DESC LIMIT 51"
    };
    let select = format!(
        "SELECT {POST_COLUMNS}, {} FROM posts p \
         JOIN users u ON u.id=p.author_id AND u.status='active' \
         WHERE p.posting_window_id=w.id AND p.deleted_at IS NULL {conditions}",
        user_columns_as("u", "author")
    );
    let candidates = if input.feed == "friends-of-friends" {
        // A generic plan estimates a small graph and probes the author index
        // once per friend-of-friend and window. Dense graphs need a hashed
        // membership filter over the current window instead. The branches are
        // mutually exclusive within this statement's snapshot.
        let membership = "p.author_id IN (SELECT id FROM friends_of_friends)";
        let dense = select.replace(membership, &format!("({membership}) IS TRUE"));
        format!(
            "({select} AND (SELECT count(*) FROM friends_of_friends)<=128 {order}) \
             UNION ALL \
             ({dense} AND (SELECT count(*) FROM friends_of_friends)>128 {order})"
        )
    } else {
        format!("{select}{order}")
    };
    if ranked {
        let query = format!(
            r#"
        {VIEWER_RELATIONS}{RANKING_RELATIONS}
        SELECT ranked.* FROM (
          SELECT fof_posts.*, (10 * coalesce(engaged.n, 0) + 3 * coalesce(mutual.n, 0)
              + least(30, coalesce(connection.n, 0)))::bigint AS score
          FROM fof_posts
          LEFT JOIN engaged ON engaged.post_id = fof_posts.id
          LEFT JOIN mutual ON mutual.id = fof_posts.author_id
          LEFT JOIN connection ON connection.id = fof_posts.author_id
        ) ranked
        WHERE $2::bigint IS NULL OR (ranked.score, ranked.posted_at, ranked.id) < ($2::bigint, $3::timestamptz, $4::text)
        ORDER BY ranked.score DESC, ranked.posted_at DESC, ranked.id DESC LIMIT 51
    "#
        )
        .replace(
            "{CANDIDATES}",
            &format!(
                "SELECT candidate.* FROM posting_windows w \
                 CROSS JOIN LATERAL ({candidates}) candidate WHERE w.ended_at IS NULL"
            ),
        );
        let mut posts: Vec<RankedPost> = sqlx::query_as_with(&query, args)
            .fetch_all(ranked_db)
            .await?;
        let has_more = posts.len() > 50;
        let mut items = Vec::with_capacity(posts.len().min(50));
        for ranked in posts.iter_mut().take(50) {
            let post = &mut ranked.post;
            post.author.set_image_key()?;
            items.push(FeedItemResponse {
                create_post_response: new_post_response(&post.post, &post.author),
                score: Some(ranked.score),
            });
        }
        return json(StatusCode::OK, GetFeedResponse { items, has_more });
    }
    // Each active window contributes at most 51 already-authorized rows. Its
    // top 51 contains every possible member of the globally ordered top 51.
    let query = format!(
        r#"
        {VIEWER_RELATIONS}
        SELECT candidate.* FROM posting_windows w
        CROSS JOIN LATERAL ({candidates}) candidate
        WHERE w.ended_at IS NULL
        ORDER BY candidate.posted_at DESC, candidate.id DESC LIMIT 51
    "#
    );
    let mut posts: Vec<PostWithAuthor> = sqlx::query_as_with(&query, args).fetch_all(&h.db).await?;
    let has_more = posts.len() > 50;
    let mut items = Vec::with_capacity(posts.len().min(50));
    for post in posts.iter_mut().take(50) {
        post.author.set_image_key()?;
        items.push(FeedItemResponse {
            create_post_response: new_post_response(&post.post, &post.author),
            score: None,
        });
    }
    json(StatusCode::OK, GetFeedResponse { items, has_more })
}

// Recommendation inputs for 10 * E + 3 * M + min(C, 30), evaluated in the same
// snapshot as visibility. mutual(x) counts the viewer's friends adjacent to x
// (x is never the viewer); C sums mutual over the author's friends; E counts
// distinct active friends with a live RealMoji or comment on the post.
const RANKING_RELATIONS: &str = r#"
, fof_posts AS MATERIALIZED (
  {CANDIDATES}
), mutual AS (
  SELECT path.id, count(*) AS n FROM (
    SELECT f.user_high_id AS id FROM friends one JOIN friendships f ON f.user_low_id = one.id
    UNION ALL
    SELECT f.user_low_id AS id FROM friends one JOIN friendships f ON f.user_high_id = one.id
  ) path
  WHERE path.id <> $1
  GROUP BY path.id
), connection AS (
  SELECT side.id, sum(mutual.n) AS n FROM (
    SELECT f.user_low_id AS id, f.user_high_id AS friend_id
    FROM friendships f WHERE f.user_low_id IN (SELECT author_id FROM fof_posts)
    UNION ALL
    SELECT f.user_high_id AS id, f.user_low_id AS friend_id
    FROM friendships f WHERE f.user_high_id IN (SELECT author_id FROM fof_posts)
  ) side
  JOIN mutual ON mutual.id = side.friend_id
  GROUP BY side.id
), engaged AS (
  SELECT reaction.post_id, count(*) AS n FROM (
    SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
    WHERE r.post_id IN (SELECT id FROM fof_posts) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
    UNION
    SELECT c.post_id, c.author_id AS user_id FROM comments c
    WHERE c.post_id IN (SELECT id FROM fof_posts) AND c.deleted_at IS NULL
  ) reaction
  JOIN friends ON friends.id = reaction.user_id
  JOIN users u ON u.id = reaction.user_id AND u.status = 'active'
  GROUP BY reaction.post_id
)
"#;

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
