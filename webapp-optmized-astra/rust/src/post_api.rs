use std::sync::Arc;

use axum::Extension;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::api_error::{Error, new_api_error};
use crate::bind::{Bind, Bindable, Binder, FileHeader};
use crate::db::f64_to_numeric;
use crate::image_signature::signed_image_url;
use crate::jpeg;
use crate::post::{Post, PostLocation, get_post};
use crate::post_access::visible_post;
use crate::posting_window::get_posting_window;
use crate::response::{json, no_content};
use crate::server::HandlerContext;
use crate::session::Session;
use crate::timefmt::rfc3339_nano;
use crate::user::{User, get_user, valid_user_text};
use crate::user_api::new_get_me_response;

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct CreatePostRequest {
    #[serde(skip)]
    pub image: Option<FileHeader>,
    #[serde(skip)]
    pub caption: String,
    #[serde(skip)]
    pub visibility: String,
    #[serde(skip)]
    pub location_kind: String,
    #[serde(skip)]
    pub latitude: Option<f64>,
    #[serde(skip)]
    pub longitude: Option<f64>,
    #[serde(skip)]
    pub locality: Option<String>,
}

impl Bindable for CreatePostRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.form_file("image", &mut self.image)?;
        b.form("caption", &mut self.caption)?;
        b.form("visibility", &mut self.visibility)?;
        b.form("location_kind", &mut self.location_kind)?;
        b.form("latitude", &mut self.latitude)?;
        b.form("longitude", &mut self.longitude)?;
        b.form("locality", &mut self.locality)
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PostAuthorResponse {
    pub id: String,
    pub username: String,
    pub display_name: String,
    pub profile_image_url: Option<String>,
    pub bio: String,
    pub account_type: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreatePostResponse {
    pub id: String,
    pub author: PostAuthorResponse,
    pub posting_window_id: String,
    pub image_url: String,
    pub caption: String,
    pub visibility: String,
    pub location: PostLocation,
    #[serde(serialize_with = "rfc3339_nano")]
    pub posted_at: DateTime<Utc>,
    pub on_time: bool,
    pub late_seconds: i64,
}

pub async fn create_post_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<CreatePostRequest>,
) -> Result<Response, Error> {
    let Some(image) = &input.image else {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post"));
    };
    if !valid_user_text(&input.caption, 0, 1000) {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post"));
    }
    match input.visibility.as_str() {
        "friends" | "friends_of_friends" | "public" => {}
        _ => return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post")),
    }
    let location = PostLocation {
        kind: input.location_kind.clone(),
        latitude: input.latitude,
        longitude: input.longitude,
        locality: input.locality.clone(),
    };
    if !location.valid() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post"));
    }
    let image_data = image.open();
    // [作問] 画像のバリデーションを全データを読んでから行なっている(改善するべき)
    let img = match jpeg::decode(image_data.clone(), 4096).await {
        Ok(img) => img,
        Err(_) => return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post")),
    };
    if img.width > 4096 || img.height > 4096 {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post"));
    }

    let image_id = Uuid::new_v4().to_string();
    let post_id = Uuid::new_v4().to_string();
    let user_columns = crate::user::USER_COLUMNS
        .iter()
        .map(|column| format!("(r.author).{column} AS \"author.{column}\""))
        .collect::<Vec<_>>()
        .join(", ");
    let mut result: crate::post::PostWithAuthor = sqlx::query_as(&format!(
        "SELECT {}, {user_columns} FROM astra_create_post($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) r \
         CROSS JOIN LATERAL (SELECT (r.post_row).*) p \
         CROSS JOIN LATERAL (SELECT (r.window_row).*) w",
        crate::post::POST_COLUMNS
    ))
    .bind(&session.user_id)
    .bind(&post_id)
    .bind(&image_id)
    .bind(&image_data[..])
    .bind(&input.caption)
    .bind(&input.visibility)
    .bind(&location.kind)
    .bind(f64_to_numeric(location.latitude))
    .bind(f64_to_numeric(location.longitude))
    .bind(&location.locality)
    .fetch_one(&h.db)
    .await
    .map_err(|err| {
        if let Some(database_error) = err.as_database_error()
            && database_error.code().as_deref() == Some("PZ001")
            && database_error.message() == "post_slot_unavailable"
        {
            new_api_error(StatusCode::CONFLICT, "post_slot_unavailable")
        } else {
            Error::from(err)
        }
    })?;
    result.author.set_image_key()?;
    json(
        StatusCode::CREATED,
        new_post_response(&result.post, &result.author),
    )
}

pub fn new_post_response(post: &Post, user: &User) -> CreatePostResponse {
    let mut response = CreatePostResponse {
        id: post.id.clone(),
        author: PostAuthorResponse {
            id: user.id.clone(),
            username: user.username.clone(),
            display_name: user.display_name.clone(),
            profile_image_url: new_get_me_response(user).profile_image_url,
            bio: user.bio.clone(),
            account_type: user.account_type.clone(),
        },
        posting_window_id: post.posting_window_id.clone(),
        image_url: signed_image_url(user, &post.image_id),
        caption: post.caption.clone(),
        visibility: post.visibility.clone(),
        location: post.post_location.clone(),
        posted_at: post.posted_at,
        on_time: post.posted_at <= post.on_time_deadline,
        late_seconds: 0,
    };
    if !response.on_time {
        response.late_seconds = (post.posted_at - post.on_time_deadline).num_seconds();
    }
    response
}

pub type GetMyCurrentPostResponse = CreatePostResponse;

pub async fn get_my_current_post_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
) -> Result<Response, Error> {
    let user_id = &session.user_id;
    let post_id: Option<String> = sqlx::query_scalar(
        r#"
   SELECT p.id FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   JOIN users u ON u.id = p.author_id
   JOIN posting_windows current ON current.region = u.region AND current.ended_at IS NULL
                              AND current.local_date = w.local_date
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
  "#,
    )
    .bind(user_id)
    .fetch_optional(&h.db)
    .await?;
    let Some(post_id) = post_id else {
        return Err(new_api_error(StatusCode::NOT_FOUND, "post_not_found"));
    };
    let post = match get_post(&h.db, &post_id).await {
        Err(err) if err.is_no_rows() => {
            return Err(new_api_error(StatusCode::NOT_FOUND, "post_not_found"));
        }
        result => result?,
    };
    let user = get_user(&h.db, user_id).await?;
    let response: GetMyCurrentPostResponse = new_post_response(&post, &user);
    json(StatusCode::OK, response)
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct GetPostRequest {
    #[serde(skip)]
    pub post_id: String,
}

impl Bindable for GetPostRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("postId", &mut self.post_id)
    }
}

pub type GetPostResponse = CreatePostResponse;

pub async fn get_post_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<GetPostRequest>,
) -> Result<Response, Error> {
    let post = match visible_post(&h.db, &session.user_id, &input.post_id).await {
        Err(err) if err.is_no_rows() => {
            return Err(new_api_error(StatusCode::NOT_FOUND, "post_not_found"));
        }
        result => result?,
    };
    let response: GetPostResponse = new_post_response(&post.post, &post.author);
    json(StatusCode::OK, response)
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct UpdatePostRequest {
    #[serde(skip)]
    pub post_id: String,
    pub caption: Option<String>,
    pub visibility: Option<String>,
}

impl Bindable for UpdatePostRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("postId", &mut self.post_id)
    }
}

pub type UpdatePostResponse = CreatePostResponse;

pub async fn update_post_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<UpdatePostRequest>,
) -> Result<Response, Error> {
    if input.caption.is_none() && input.visibility.is_none() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post"));
    }
    if input
        .caption
        .as_ref()
        .is_some_and(|caption| !valid_user_text(caption, 0, 1000))
    {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post"));
    }
    if let Some(visibility) = &input.visibility {
        match visibility.as_str() {
            "friends" | "friends_of_friends" | "public" => {}
            _ => return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_post")),
        }
    }
    let user_id = &session.user_id;
    let mut tx = h.db.begin().await?;
    let _locked_user_id: String =
        sqlx::query_scalar("SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE")
            .bind(user_id)
            .fetch_one(&mut *tx)
            .await?;
    let post = match get_post(&mut *tx, &input.post_id).await {
        Err(err) if err.is_no_rows() => {
            return Err(new_api_error(StatusCode::NOT_FOUND, "post_not_found"));
        }
        result => result?,
    };
    if post.author_id != *user_id {
        return Err(new_api_error(StatusCode::FORBIDDEN, "post_not_editable"));
    }
    let window = get_posting_window(&mut *tx, &post.posting_window_id).await?;
    if window.ended_at.is_some() {
        return Err(new_api_error(StatusCode::FORBIDDEN, "post_not_editable"));
    }
    if let Some(caption) = &input.caption {
        sqlx::query("UPDATE posts SET caption = $2 WHERE id = $1")
            .bind(&post.id)
            .bind(caption)
            .execute(&mut *tx)
            .await?;
    }
    if let Some(visibility) = &input.visibility {
        sqlx::query("UPDATE posts SET visibility = $2 WHERE id = $1")
            .bind(&post.id)
            .bind(visibility)
            .execute(&mut *tx)
            .await?;
    }
    let post = get_post(&mut *tx, &post.id).await?;
    let user = get_user(&mut *tx, user_id).await?;
    tx.commit().await?;
    let response: UpdatePostResponse = new_post_response(&post, &user);
    json(StatusCode::OK, response)
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct DeletePostRequest {
    #[serde(skip)]
    pub post_id: String,
}

impl Bindable for DeletePostRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("postId", &mut self.post_id)
    }
}

pub async fn delete_post_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<DeletePostRequest>,
) -> Result<Response, Error> {
    let user_id = &session.user_id;
    let mut tx = h.db.begin().await?;
    let _locked_user_id: String =
        sqlx::query_scalar("SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE")
            .bind(user_id)
            .fetch_one(&mut *tx)
            .await?;
    let post = match get_post(&mut *tx, &input.post_id).await {
        Err(err) if err.is_no_rows() => {
            return Err(new_api_error(StatusCode::NOT_FOUND, "post_not_found"));
        }
        result => result?,
    };
    if post.author_id != *user_id {
        return Err(new_api_error(StatusCode::FORBIDDEN, "post_not_editable"));
    }
    let window = get_posting_window(&mut *tx, &post.posting_window_id).await?;
    let deletions: i64 = sqlx::query_scalar(
        r#"
   SELECT count(*) FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
   WHERE p.author_id = $1 AND w.local_date = $2 AND p.deleted_at IS NOT NULL
  "#,
    )
    .bind(user_id)
    .bind(window.local_date)
    .fetch_one(&mut *tx)
    .await?;
    if deletions >= 2 {
        return Err(new_api_error(StatusCode::CONFLICT, "post_slot_unavailable"));
    }
    sqlx::query("UPDATE posts SET deleted_at = clock_timestamp() WHERE id = $1")
        .bind(&post.id)
        .execute(&mut *tx)
        .await?;
    tx.commit().await?;
    no_content(StatusCode::NO_CONTENT)
}
