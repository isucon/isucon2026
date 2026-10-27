use std::sync::Arc;

use axum::Extension;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use serde::{Deserialize, Serialize};

use crate::api_error::{Error, new_api_error};
use crate::bind::{Bind, Bindable, Binder};
use crate::post::get_friend_ids;
use crate::relationship::is_blocked;
use crate::relationship_list_api::RelationshipUserResponse;
use crate::response::json;
use crate::search_text;
use crate::server::HandlerContext;
use crate::session::Session;
use crate::user::{USER_COLUMNS, User, get_user, valid_user_text};
use crate::user_api::new_get_me_response;

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct SearchUsersRequest {
    #[serde(skip)]
    pub q: String,
    #[serde(skip)]
    pub before_username: String,
    #[serde(skip)]
    pub before_id: String,
}

impl Bindable for SearchUsersRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.query("q", &mut self.q)?;
        b.query("beforeUsername", &mut self.before_username)?;
        b.query("beforeId", &mut self.before_id)
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SearchUsersResponse {
    pub items: Vec<RelationshipUserResponse>,
    pub has_more: bool,
}

pub async fn search_users_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<SearchUsersRequest>,
) -> Result<Response, Error> {
    if !valid_user_text(&input.q, 1, 100) {
        return Err(new_api_error(
            StatusCode::BAD_REQUEST,
            "invalid_relationship",
        ));
    }
    if input.before_username.is_empty() != input.before_id.is_empty() {
        return Err(new_api_error(StatusCode::BAD_REQUEST, "invalid_pagination"));
    }
    let viewer = &session.user_id;
    let query = search_text::normalize(&input.q);
    // C collation folds ASCII exactly like Rust. Retain every non-ASCII name
    // for the full NFC/case-fold check below (for example, ß can match "ss").
    // An ASCII-only name cannot match a normalized non-ASCII query.
    // PostgreSQL text cannot contain NUL; such queries cannot match stored names.
    let ascii_pattern = (query.is_ascii() && !query.contains('\0')).then(|| {
        format!(
            "%{}%",
            query
                .replace('!', "!!")
                .replace('%', "!%")
                .replace('_', "!_")
        )
    });
    // Keep candidate matching and returned account fields in one snapshot.
    // Most candidates do not match; their bio, key, and other large fields
    // need not be transferred from PostgreSQL or decoded by SQLx.
    let mut tx =
        h.db.begin_with("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .await?;
    #[derive(sqlx::FromRow)]
    struct Candidate {
        id: String,
        username: String,
        display_name: String,
    }
    let candidates: Vec<Candidate> = sqlx::query_as(
        "SELECT u.id, u.username, u.display_name FROM users u \
         WHERE u.status='active' AND u.id<>$1 \
         AND ($2='' OR (u.username,u.id)>($2,$3)) \
         AND (octet_length(convert_to(u.username,'UTF8'))<>char_length(u.username) \
           OR octet_length(convert_to(u.display_name,'UTF8'))<>char_length(u.display_name) \
           OR lower(u.username COLLATE \"C\") LIKE $4 ESCAPE '!' \
           OR lower(u.display_name COLLATE \"C\") LIKE $4 ESCAPE '!') \
         AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocker_id=$1 AND b.blocked_id=u.id) \
         AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocked_id=$1 AND b.blocker_id=u.id) \
         ORDER BY u.username,u.id")
        .bind(viewer).bind(&input.before_username).bind(&input.before_id).bind(&ascii_pattern)
        .fetch_all(&mut *tx).await?;
    let ids: Vec<String> = candidates
        .into_iter()
        .filter(|user| {
            search_text::matches(&user.username, &query)
                || search_text::matches(&user.display_name, &query)
        })
        .take(51)
        .map(|user| user.id)
        .collect();
    let users: Vec<User> = if ids.is_empty() {
        Vec::new()
    } else {
        sqlx::query_as(&format!(
            "SELECT {} FROM users WHERE id=ANY($1) ORDER BY username,id",
            USER_COLUMNS.join(", ")
        ))
        .bind(&ids)
        .fetch_all(&mut *tx)
        .await?
    };
    tx.commit().await?;
    let mut items = Vec::new();
    for mut user in users {
        user.set_image_key()?;
        if user.account_type != "certified_public" {
            user.bio = String::new();
        }
        items.push(new_relationship_user_response(&user));
        if items.len() == 51 {
            break;
        }
    }
    let has_more = items.len() > 50;
    if has_more {
        items.truncate(50);
    }
    json(StatusCode::OK, SearchUsersResponse { items, has_more })
}

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct GetUserRequest {
    #[serde(skip)]
    pub user_id: String,
}

impl Bindable for GetUserRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("userId", &mut self.user_id)
    }
}

pub type GetUserResponse = RelationshipUserResponse;

pub async fn get_user_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
    Bind(input): Bind<GetUserRequest>,
) -> Result<Response, Error> {
    let viewer = &session.user_id;
    let mut user = match get_user(&h.db, &input.user_id).await {
        Err(err) if err.is_no_rows() => {
            return Err(new_api_error(
                StatusCode::NOT_FOUND,
                "relationship_not_found",
            ));
        }
        result => result?,
    };
    let blocked = is_blocked(&h.db, viewer, &user.id).await?;
    if blocked {
        return Err(new_api_error(
            StatusCode::NOT_FOUND,
            "relationship_not_found",
        ));
    }
    let friends = get_friend_ids(&h.db, viewer).await?;
    if user.account_type != "certified_public" && !friends.contains(&user.id) {
        user.bio = String::new();
    }
    let response: GetUserResponse = new_relationship_user_response(&user);
    json(StatusCode::OK, response)
}

pub fn new_relationship_user_response(user: &User) -> RelationshipUserResponse {
    RelationshipUserResponse {
        id: user.id.clone(),
        username: user.username.clone(),
        display_name: user.display_name.clone(),
        profile_image_url: new_get_me_response(user).profile_image_url,
        bio: user.bio.clone(),
        account_type: user.account_type.clone(),
    }
}
