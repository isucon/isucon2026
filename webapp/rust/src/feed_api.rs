use std::collections::HashSet;
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
use crate::post::{Post, ViewerContext, load_viewer_context, select_posts_with_author};
use crate::post_api::{CreatePostResponse, new_post_response};
use crate::response::json;
use crate::server::HandlerContext;
use crate::session::Session;

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
    let viewer = match load_viewer_context(&mut *h.db.acquire().await?, &session.user_id).await {
        Err(err) if err.is_no_rows() => {
            return json(
                StatusCode::OK,
                GetFeedResponse {
                    items: Vec::new(),
                    has_more: false,
                },
            );
        }
        result => result?,
    };
    let mut following = HashSet::new();
    if input.feed == "following" {
        let followee_ids: Vec<String> =
            sqlx::query_scalar("SELECT followee_id FROM follows WHERE follower_id = $1")
                .bind(&viewer.user.id)
                .fetch_all(&h.db)
                .await?;
        following.extend(followee_ids);
    }
    let posts = select_posts_with_author(
        &h.db,
        r#"
   AND p.author_id <> $1 AND w.ended_at IS NULL
   AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
   ORDER BY p.posted_at DESC, p.id DESC
  "#,
        args![&viewer.user.id, input.before_posted_at, &input.before_id],
    )
    .await?;
    let mut items = Vec::new();
    for post in &posts {
        if !viewer.can_view(&post.post)
            || !matches_feed(&viewer, &following, &post.post, &input.feed)
        {
            continue;
        }
        items.push(new_post_response(&post.post, &post.author));
        if items.len() == 51 {
            break;
        }
    }
    let has_more = items.len() > 50;
    if has_more {
        items.truncate(50);
    }
    json(StatusCode::OK, GetFeedResponse { items, has_more })
}

fn matches_feed(
    viewer: &ViewerContext,
    following: &HashSet<String>,
    post: &Post,
    feed: &str,
) -> bool {
    match feed {
        "public" => post.visibility == "public",
        "following" => following.contains(&post.author_id),
        "friends" => viewer.friends.contains(&post.author_id),
        "friends-of-friends" => {
            !viewer.friends.contains(&post.author_id)
                && viewer.friends_of_friends.contains(&post.author_id)
        }
        _ => false,
    }
}
