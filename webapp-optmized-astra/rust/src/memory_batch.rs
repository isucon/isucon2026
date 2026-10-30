//! Read one authorized history page and its visible interactions in one snapshot.
use std::collections::HashMap;

use axum::http::StatusCode;
use chrono::{DateTime, NaiveDate, Utc};
use sqlx::{FromRow, PgPool, Row};

use crate::api_error::{Error, new_api_error};
use crate::comment::Comment;
use crate::comment_api::new_comment_response;
use crate::memory_api::{GetMemoriesResponse, MemoryPostResponse};
use crate::post::{POST_COLUMNS, PostWithAuthor};
use crate::post_api::new_post_response;
use crate::realmoji::RealMoji;
use crate::realmoji_api::new_real_moji_response;
use crate::user::{User, user_columns_as};

pub(crate) async fn list_memories(
    db: &PgPool,
    owner: &str,
    start: NaiveDate,
    end: NaiveDate,
    before_at: Option<DateTime<Utc>>,
    before_id: &str,
) -> Result<GetMemoriesResponse, Error> {
    if before_at.is_none() != before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let rows = sqlx::query(&format!(
        r#"
        WITH selected AS MATERIALIZED (
          SELECT {POST_COLUMNS}, {} FROM posts p
          JOIN posting_windows w ON w.id=p.posting_window_id
          JOIN users u ON u.id=p.author_id AND u.status='active'
          WHERE p.author_id=$1 AND p.deleted_at IS NULL AND w.local_date >= $2 AND w.local_date < $3
            AND ($4::timestamptz IS NULL OR (p.posted_at,p.id)<($4,$5))
          ORDER BY p.posted_at DESC,p.id DESC LIMIT 51
        )
        SELECT p.*, (SELECT count(*)>50 FROM selected) AS has_more, i.*
        FROM (SELECT * FROM selected ORDER BY posted_at DESC,id DESC LIMIT 50) p
        LEFT JOIN LATERAL (
          (SELECT 0 AS kind, c.id AS interaction_id, c.body AS interaction_body,
                  NULL::text AS interaction_image_id, NULL::text AS interaction_reaction_type,
                  c.created_at AS interaction_created_at, {actor}
           FROM comments c JOIN users u ON u.id=c.author_id AND u.status='active'
           WHERE c.post_id=p.id AND c.deleted_at IS NULL
           ORDER BY c.created_at,c.id LIMIT 50)
          UNION ALL
          (SELECT 1, r.id, NULL::text, r.image_id, r.reaction_type, r.created_at, {actor}
           FROM realmojis r JOIN users u ON u.id=r.reactor_id AND u.status='active'
           WHERE r.post_id=p.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
           ORDER BY r.created_at DESC,r.id DESC LIMIT 50)
        ) i ON true
    "#,
        user_columns_as("u", "author"),
        actor = user_columns_as("u", "actor")
    ))
    .bind(owner)
    .bind(start)
    .bind(end)
    .bind(before_at)
    .bind(before_id)
    .fetch_all(db)
    .await?;
    // fetch_all returns the pool connection before decoding and URL signing.
    // Group and order the bounded page here rather than making PostgreSQL build JSON.
    let mut response = GetMemoriesResponse::default();
    let mut indices = HashMap::new();
    for row in rows {
        response.has_more = row.try_get("has_more")?;
        let id: &str = row.try_get("id")?;
        let index = match indices.get(id) {
            Some(&index) => index,
            None => {
                let mut post = PostWithAuthor::from_row(&row)?;
                post.author.set_image_key()?;
                let index = response.items.len();
                response.items.push(MemoryPostResponse {
                    create_post_response: new_post_response(&post.post, &post.author),
                    comments: Vec::new(),
                    realmojis: Vec::new(),
                });
                indices.insert(id.to_owned(), index);
                index
            }
        };
        let Some(kind) = row.try_get::<Option<i32>, _>("kind")? else {
            continue;
        };
        let mut actor = User::from_row_with_prefix(&row, "actor")?;
        actor.set_image_key()?;
        let item = &mut response.items[index];
        if kind == 0 {
            let comment = Comment {
                id: row.try_get("interaction_id")?,
                post_id: id.to_owned(),
                author_id: actor.id.clone(),
                body: row.try_get("interaction_body")?,
                created_at: row.try_get("interaction_created_at")?,
                deleted_at: None,
            };
            item.comments.push(new_comment_response(&comment, &actor));
        } else {
            let reaction = RealMoji {
                id: row.try_get("interaction_id")?,
                post_id: id.to_owned(),
                reactor_id: actor.id.clone(),
                image_id: row.try_get("interaction_image_id")?,
                reaction_type: row.try_get("interaction_reaction_type")?,
                created_at: row.try_get("interaction_created_at")?,
                replaced_at: None,
                deleted_at: None,
            };
            item.realmojis
                .push(new_real_moji_response(&reaction, &actor));
        }
    }
    for item in &mut response.items {
        item.comments
            .sort_unstable_by(|a, b| (a.created_at, &a.id).cmp(&(b.created_at, &b.id)));
        item.realmojis
            .sort_unstable_by(|a, b| (b.created_at, &b.id).cmp(&(a.created_at, &a.id)));
    }
    response.items.sort_unstable_by(|a, b| {
        let a = &a.create_post_response;
        let b = &b.create_post_response;
        (b.posted_at, &b.id).cmp(&(a.posted_at, &a.id))
    });
    Ok(response)
}

#[cfg(test)]
#[path = "memory_batch_tests.rs"]
mod tests;
