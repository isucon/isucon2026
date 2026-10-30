//! Read one authorized history page and its visible interactions in one snapshot.
use axum::http::StatusCode;
use chrono::{DateTime, NaiveDate, Utc};
use sqlx::{FromRow, PgConnection, Row};

use crate::api_error::{Error, new_api_error};
use crate::comment::CommentWithAuthor;
use crate::comment_api::new_comment_response;
use crate::memory_api::{GetMemoriesResponse, MemoryPostResponse};
use crate::post::{POST_COLUMNS, PostWithAuthor};
use crate::post_api::new_post_response;
use crate::realmoji::RealMojiWithReactor;
use crate::realmoji_api::new_real_moji_response;
use crate::user::user_columns_as;

pub(crate) async fn list_memories(
    db: &mut PgConnection,
    owner: &str,
    start: NaiveDate,
    end: NaiveDate,
    before_at: Option<DateTime<Utc>>,
    before_id: &str,
) -> Result<GetMemoriesResponse, Error> {
    if before_at.is_none() != before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let rows = sqlx::query(&format!(r#"
        WITH selected AS MATERIALIZED (
          SELECT {POST_COLUMNS}, {} FROM posts p
          JOIN posting_windows w ON w.id=p.posting_window_id
          JOIN users u ON u.id=p.author_id AND u.status='active'
          WHERE p.author_id=$1 AND p.deleted_at IS NULL AND w.local_date >= $2 AND w.local_date < $3
            AND ($4::timestamptz IS NULL OR (p.posted_at,p.id)<($4,$5))
          ORDER BY p.posted_at DESC,p.id DESC LIMIT 51
        )
        SELECT selected.*, (SELECT count(*)>50 FROM selected) AS has_more,
          coalesce((SELECT json_agg(i.item ORDER BY i.created_at,i.id)::text FROM (
            SELECT c.created_at,c.id,json_build_object('comment',row_to_json(c),'author',row_to_json(u)) AS item
            FROM comments c JOIN users u ON u.id=c.author_id AND u.status='active'
            WHERE c.post_id=selected.id AND c.deleted_at IS NULL
            ORDER BY c.created_at,c.id LIMIT 50
          ) i),'[]') AS comments_json,
          coalesce((SELECT json_agg(i.item ORDER BY i.created_at DESC,i.id DESC)::text FROM (
            SELECT r.created_at,r.id,json_build_object('real_moji',row_to_json(r),'reactor',row_to_json(u)) AS item
            FROM realmojis r JOIN users u ON u.id=r.reactor_id AND u.status='active'
            WHERE r.post_id=selected.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
            ORDER BY r.created_at DESC,r.id DESC LIMIT 50
          ) i),'[]') AS reactions_json
        FROM selected ORDER BY posted_at DESC,id DESC LIMIT 50
    "#, user_columns_as("u", "author")))
        .bind(owner).bind(start).bind(end).bind(before_at).bind(before_id)
        .fetch_all(db).await?;
    let mut response = GetMemoriesResponse::default();
    for row in rows {
        response.has_more = row.try_get("has_more")?;
        let mut post = PostWithAuthor::from_row(&row)?;
        post.author.set_image_key()?;
        let mut comments: Vec<CommentWithAuthor> =
            serde_json::from_str(row.try_get("comments_json")?)?;
        let mut reactions: Vec<RealMojiWithReactor> =
            serde_json::from_str(row.try_get("reactions_json")?)?;
        let mut item = MemoryPostResponse {
            create_post_response: new_post_response(&post.post, &post.author),
            comments: Vec::with_capacity(comments.len()),
            realmojis: Vec::with_capacity(reactions.len()),
        };
        for c in &mut comments {
            c.author.set_image_key()?;
            item.comments
                .push(new_comment_response(&c.comment, &c.author));
        }
        for r in &mut reactions {
            r.reactor.set_image_key()?;
            item.realmojis
                .push(new_real_moji_response(&r.real_moji, &r.reactor));
        }
        response.items.push(item);
    }
    Ok(response)
}

#[cfg(test)]
#[path = "memory_batch_tests.rs"]
mod tests;
