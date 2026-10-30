use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use axum::Extension;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use chrono::{DateTime, Days, Months, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use sqlx::postgres::PgArguments;
use sqlx::{Arguments, PgConnection};

use crate::api_error::{Error, new_api_error};
use crate::bind::{Bind, Bindable, Binder};
use crate::comment::select_comments_with_author;
use crate::comment_api::{CreateCommentResponse, new_comment_response};
use crate::db::{args, in_placeholders};
use crate::post::select_posts_with_author;
use crate::post_api::{CreatePostResponse, new_post_response};
use crate::realmoji::select_current_real_mojis;
use crate::realmoji_api::{ReplaceRealMojiResponse, new_real_moji_response};
use crate::response::json;
use crate::server::HandlerContext;
use crate::session::Session;
use crate::timefmt::{parse_date_only, parse_year_month};

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct GetMemoriesRequest {
    #[serde(skip)]
    pub month: String,
    #[serde(skip)]
    pub before_posted_at: Option<DateTime<Utc>>,
    #[serde(skip)]
    pub before_id: String,
}

impl Bindable for GetMemoriesRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.query("month", &mut self.month)?;
        b.query("beforePostedAt", &mut self.before_posted_at)?;
        b.query("beforeId", &mut self.before_id)
    }
}

#[derive(Serialize)]
pub struct MemoryPostResponse {
    #[serde(flatten)]
    pub create_post_response: CreatePostResponse,
    pub realmojis: Vec<ReplaceRealMojiResponse>,
    pub comments: Vec<CreateCommentResponse>,
}

#[derive(Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct GetMemoriesResponse {
    pub items: Vec<MemoryPostResponse>,
    pub has_more: bool,
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct GetMemoriesByDateRequest {
    #[serde(skip)]
    pub date: String,
    #[serde(skip)]
    pub before_posted_at: Option<DateTime<Utc>>,
    #[serde(skip)]
    pub before_id: String,
}

impl Bindable for GetMemoriesByDateRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("date", &mut self.date)?;
        b.query("beforePostedAt", &mut self.before_posted_at)?;
        b.query("beforeId", &mut self.before_id)
    }
}

pub type GetMemoriesByDateResponse = GetMemoriesResponse;

pub async fn get_memories_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<GetMemoriesRequest>,
) -> Result<Response, Error> {
    let Some(start) = parse_year_month(&input.month) else {
        return Err(new_api_error(
            StatusCode::BAD_REQUEST,
            "invalid_memory_period",
        ));
    };
    let response = cached_memories(
        &h.db,
        &session.user_id,
        start,
        start + Months::new(1),
        input.before_posted_at,
        &input.before_id,
    )
    .await?;
    json(StatusCode::OK, response)
}

pub async fn get_memories_by_date_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<GetMemoriesByDateRequest>,
) -> Result<Response, Error> {
    let Some(start) = parse_date_only(&input.date) else {
        return Err(new_api_error(
            StatusCode::BAD_REQUEST,
            "invalid_memory_period",
        ));
    };
    let response: GetMemoriesByDateResponse = cached_memories(
        &h.db,
        &session.user_id,
        start,
        start + Days::new(1),
        input.before_posted_at,
        &input.before_id,
    )
    .await?;
    json(StatusCode::OK, response)
}

pub async fn list_memories(
    db: &mut PgConnection,
    user_id: &str,
    start: NaiveDate,
    end: NaiveDate,
    before_at: Option<DateTime<Utc>>,
    before_id: &str,
) -> Result<GetMemoriesResponse, Error> {
    let mut response = GetMemoriesResponse::default();
    if before_at.is_none() != before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let posts = select_posts_with_author(
        &mut *db,
        r#"
  AND p.author_id = $1 AND w.local_date >= $2 AND w.local_date < $3
  AND ($4::timestamptz IS NULL OR (p.posted_at, p.id) < ($4, $5))
  ORDER BY p.posted_at DESC, p.id DESC LIMIT 51
 "#,
        args![user_id, start, end, before_at, before_id],
    )
    .await?;
    let mut items = Vec::with_capacity(posts.len());
    for post in &posts {
        items.push(MemoryPostResponse {
            create_post_response: new_post_response(&post.post, &post.author),
            realmojis: Vec::new(),
            comments: Vec::new(),
        });
    }
    response.has_more = items.len() > 50;
    if response.has_more {
        items.truncate(50);
    }
    load_memory_interactions(db, &mut items).await?;
    response.items = items;
    Ok(response)
}

pub async fn cached_memories(
    db: &sqlx::PgPool,
    user_id: &str,
    start: NaiveDate,
    end: NaiveDate,
    before_at: Option<DateTime<Utc>>,
    before_id: &str,
) -> Result<GetMemoriesResponse, Error> {
    if before_at.is_none() != before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let history = crate::history_cache::load(db, user_id).await?;
    let mut posts: Vec<_> = history
        .posts
        .iter()
        .filter(|p| {
            p.date >= start
                && p.date < end
                && before_at.is_none_or(|before| {
                    (p.post.posted_at, p.post.id.as_str()) < (before, before_id)
                })
        })
        .take(51)
        .collect();
    let has_more = posts.len() > 50;
    posts.truncate(50);
    if posts.is_empty() {
        return Ok(GetMemoriesResponse::default());
    }
    let mut ids = HashSet::from([user_id]);
    for post in &posts {
        if let Some(comments) = history.comments.get(&post.post.id) {
            ids.extend(comments.iter().map(|c| c.author_id.as_str()));
        }
        if let Some(reactions) = history.reactions.get(&post.post.id) {
            ids.extend(reactions.iter().map(|r| r.reactor_id.as_str()));
        }
    }
    // Do not cache user liveness, names, profile images, or signing keys. A
    // single indexed lookup reflects changes without broad invalidation.
    let ids: Vec<_> = ids.into_iter().collect();
    let users: Vec<crate::user::User> = sqlx::query_as(&format!(
        "SELECT {} FROM users WHERE id=ANY($1) AND status='active'",
        crate::user::USER_COLUMNS.join(",")
    ))
    .bind(&ids)
    .fetch_all(db)
    .await?;
    let mut by_id = HashMap::new();
    for mut user in users {
        user.set_image_key()?;
        by_id.insert(user.id.clone(), user);
    }
    let Some(author) = by_id.get(user_id) else {
        return Ok(GetMemoriesResponse::default());
    };
    let items = posts
        .into_iter()
        .map(|post| {
            let comments = history
                .comments
                .get(&post.post.id)
                .into_iter()
                .flatten()
                .filter_map(|c| by_id.get(&c.author_id).map(|u| new_comment_response(c, u)))
                .take(50)
                .collect();
            let realmojis = history
                .reactions
                .get(&post.post.id)
                .into_iter()
                .flatten()
                .filter_map(|r| {
                    by_id
                        .get(&r.reactor_id)
                        .map(|u| new_real_moji_response(r, u))
                })
                .take(50)
                .collect();
            MemoryPostResponse {
                create_post_response: new_post_response(&post.post, author),
                comments,
                realmojis,
            }
        })
        .collect();
    Ok(GetMemoriesResponse { items, has_more })
}

async fn load_memory_interactions(
    db: &mut PgConnection,
    items: &mut [MemoryPostResponse],
) -> Result<(), Error> {
    if items.is_empty() {
        return Ok(());
    }
    let post_ids: Vec<String> = items
        .iter()
        .map(|item| item.create_post_response.id.clone())
        .collect();
    let item_by_post_id: HashMap<String, usize> = post_ids
        .iter()
        .enumerate()
        .map(|(i, post_id)| (post_id.clone(), i))
        .collect();

    let mut args = PgArguments::default();
    for post_id in &post_ids {
        args.add(post_id).map_err(|err| anyhow::anyhow!(err))?;
    }
    let reactions = select_current_real_mojis(
        &mut *db,
        &format!(
            "AND r.post_id IN ({}) ORDER BY r.created_at DESC, r.id DESC",
            in_placeholders(1, post_ids.len())
        ),
        args,
    )
    .await?;
    for reaction in &reactions {
        let item = &mut items[item_by_post_id[&reaction.real_moji.post_id]];
        if item.realmojis.len() < 50 {
            item.realmojis.push(new_real_moji_response(
                &reaction.real_moji,
                &reaction.reactor,
            ));
        }
    }

    let mut args = PgArguments::default();
    for post_id in &post_ids {
        args.add(post_id).map_err(|err| anyhow::anyhow!(err))?;
    }
    let comments = select_comments_with_author(
        &mut *db,
        &format!(
            "AND c.post_id IN ({}) ORDER BY c.created_at, c.id",
            in_placeholders(1, post_ids.len())
        ),
        args,
    )
    .await?;
    for comment in &comments {
        let item = &mut items[item_by_post_id[&comment.comment.post_id]];
        if item.comments.len() < 50 {
            item.comments
                .push(new_comment_response(&comment.comment, &comment.author));
        }
    }
    Ok(())
}
