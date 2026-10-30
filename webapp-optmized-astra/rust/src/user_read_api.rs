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
    // PostgreSQL text cannot contain NUL, so no stored name can match it.
    if query.contains('\0') {
        return json(
            StatusCode::OK,
            SearchUsersResponse {
                items: Vec::new(),
                has_more: false,
            },
        );
    }
    let pattern = format!(
        "%{}%",
        query
            .replace('!', "!!")
            .replace('%', "!%")
            .replace('_', "!_")
    );
    // A normal page fits in one statement. Keep the full row fetch after the
    // candidate limit, and validate dirty Unicode names with the same Rust
    // normalization as the fallback. If dirty nonmatches consume the page,
    // restart the existing paginated lookup in its own consistent snapshot.
    let fast_sql = format!(
        r#"WITH candidate_ids AS MATERIALIZED (
        SELECT user_id FROM astra_user_search
        WHERE normalized_username LIKE $4 ESCAPE '!'
           OR normalized_display_name LIKE $4 ESCAPE '!'
        UNION SELECT user_id FROM astra_search_dirty
      ), page AS MATERIALIZED (
        SELECT u.id,u.username FROM candidate_ids c JOIN users u ON u.id=c.user_id
        WHERE u.status='active' AND u.id<>$1
          AND ($2='' OR (u.username,u.id)>($2,$3))
          AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocker_id=$1 AND b.blocked_id=u.id)
          AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocked_id=$1 AND b.blocker_id=u.id)
        ORDER BY u.username,u.id LIMIT 64
      ) SELECT {} FROM page JOIN users u ON u.id=page.id ORDER BY page.username,page.id"#,
        USER_COLUMNS
            .iter()
            .map(|column| format!("u.{column}"))
            .collect::<Vec<_>>()
            .join(",")
    );
    let mut fast_users: Vec<User> = sqlx::query_as(&fast_sql)
        .persistent(false)
        .bind(viewer)
        .bind(&input.before_username)
        .bind(&input.before_id)
        .bind(&pattern)
        .fetch_all(&h.db)
        .await?;
    let exhausted = fast_users.len() < 64;
    fast_users.retain(|user| {
        search_text::matches(&user.username, &query)
            || search_text::matches(&user.display_name, &query)
    });
    let users = if exhausted || fast_users.len() >= 51 {
        fast_users.truncate(51);
        fast_users
    } else {
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
        // Match immutable derived text first. Name changes, missing derived rows,
        // and reset mark users dirty in the same transaction, so the fallback
        // below preserves coverage until Rust has rebuilt their normalization.
        let mut cursor = (input.before_username.clone(), input.before_id.clone());
        let mut ids = Vec::with_capacity(51);
        let mut batch_size = 64_i64;
        loop {
            let candidates: Vec<Candidate> = sqlx::query_as(
        r#"WITH candidate_ids AS MATERIALIZED (
             SELECT user_id FROM astra_user_search
             WHERE normalized_username LIKE $4 ESCAPE '!'
                OR normalized_display_name LIKE $4 ESCAPE '!'
             UNION SELECT user_id FROM astra_search_dirty
           )
           SELECT u.id,u.username,u.display_name
           FROM candidate_ids c JOIN users u ON u.id=c.user_id
           WHERE u.status='active' AND u.id<>$1
             AND ($2='' OR (u.username,u.id)>($2,$3))
             AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocker_id=$1 AND b.blocked_id=u.id)
             AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocked_id=$1 AND b.blocker_id=u.id)
           ORDER BY u.username,u.id LIMIT $5"#)
        // Broad and selective substrings need different PostgreSQL plans.
        // An unnamed statement keeps planning sensitive to the current pattern.
        .persistent(false)
        .bind(viewer).bind(&cursor.0).bind(&cursor.1).bind(&pattern).bind(batch_size)
        .fetch_all(&mut *tx).await?;
            let exhausted = candidates.len() < batch_size as usize;
            if let Some(last) = candidates.last() {
                cursor = (last.username.clone(), last.id.clone());
            }
            for user in candidates {
                if search_text::matches(&user.username, &query)
                    || search_text::matches(&user.display_name, &query)
                {
                    ids.push(user.id);
                    if ids.len() == 51 {
                        break;
                    }
                }
            }
            if ids.len() == 51 || exhausted {
                break;
            }
            // A rebuild/import can temporarily leave many dirty candidates. Bound
            // memory while avoiding hundreds of roundtrips through that fallback.
            batch_size = (batch_size * 2).min(4096);
        }
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
        users
    };
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
