use std::collections::HashMap;
use std::convert::Infallible;
use std::io::Write;
use std::sync::{Arc, LazyLock, Mutex};

use axum::extract::{FromRequestParts, MatchedPath, Request, State};
use axum::http::Method;
use axum::http::request::Parts;
use axum::middleware::Next;
use axum::response::Response;
use serde::Serialize;

use crate::ip_ban::extract_client_ip;
use crate::session::Session;
use crate::user::USERNAME_PATTERN;

static ACTION_NAMES: LazyLock<HashMap<(&str, &str), &str>> = LazyLock::new(|| {
    [
        ("POST /api/initialize", "initialize"),
        ("POST /api/auth/register", "register"),
        ("POST /api/auth/login", "login"),
        ("POST /api/auth/logout", "logout"),
        ("GET /api/users", "search_users"),
        ("GET /api/users/{userId}", "get_user"),
        ("GET /api/me/export", "export_posts"),
        ("GET /api/me", "get_me"),
        ("PATCH /api/me", "update_me"),
        ("PUT /api/me/profile-image", "put_profile_image"),
        ("DELETE /api/me/profile-image", "delete_profile_image"),
        ("POST /api/me/account-deletion", "delete_account"),
        ("POST /api/posts", "create_post"),
        (
            "GET /api/posting-window/current",
            "get_current_posting_window",
        ),
        ("GET /api/posts/me/current", "get_my_current_post"),
        ("GET /api/posts/{postId}", "get_post"),
        ("PATCH /api/posts/{postId}", "update_post"),
        ("DELETE /api/posts/{postId}", "delete_post"),
        ("GET /api/feeds/{feed}", "get_feed"),
        ("GET /api/memories", "get_memories"),
        ("GET /api/memories/{date}", "get_memories_by_date"),
        ("POST /api/posts/{postId}/comments", "create_comment"),
        ("GET /api/posts/{postId}/comments", "get_comments"),
        ("DELETE /api/comments/{commentId}", "delete_comment"),
        ("POST /api/posts/{postId}/realmojis", "replace_real_moji"),
        ("GET /api/posts/{postId}/realmojis", "get_real_mojis"),
        ("DELETE /api/realmojis/{realMojiId}", "delete_real_moji"),
        ("GET /api/images/{imageId}", "get_image"),
        ("GET /api/notifications", "get_notifications"),
        ("POST /api/friend-requests", "create_friend_request"),
        (
            "POST /api/friend-requests/{requestId}/{action}",
            "transition_friend_request",
        ),
        ("GET /api/relationships/{kind}", "get_relationships"),
        ("DELETE /api/friendships/{userId}", "delete_friendship"),
        ("PUT /api/follows/{userId}", "follow"),
        ("DELETE /api/follows/{userId}", "unfollow"),
        ("PUT /api/blocks/{userId}", "block"),
        ("DELETE /api/blocks/{userId}", "unblock"),
        ("POST /api/admin/auth/login", "admin_login"),
        ("POST /api/admin/auth/logout", "admin_logout"),
        (
            "PUT /api/admin/users/{userId}/certification",
            "certify_user",
        ),
        (
            "DELETE /api/admin/users/{userId}/certification",
            "delete_user_certification",
        ),
        ("POST /api/admin/posting-window", "open_posting_window"),
    ]
    .into_iter()
    .map(|(route, action)| (route.split_once(' ').expect("method and route"), action))
    .collect()
});

fn log_level_rank(level: &str) -> u8 {
    match level {
        "info" => 1,
        "error" => 2,
        _ => 0,
    }
}

#[derive(Serialize)]
struct ActionEntry<'a> {
    level: &'static str,
    action: Option<&'static str>,
    status: u16,
    ip: &'a str,
    user_id: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    username: Option<Option<&'a str>>,
}

#[derive(Clone)]
pub struct ActionLog {
    min_level: u8,
    output: Arc<Mutex<dyn Write + Send>>,
}

impl ActionLog {
    pub fn new(min_level: &str, output: impl Write + Send + 'static) -> ActionLog {
        ActionLog {
            min_level: log_level_rank(min_level),
            output: Arc::new(Mutex::new(output)),
        }
    }
}

#[derive(Clone, Default)]
pub struct Username(Arc<Mutex<Option<String>>>);

impl Username {
    pub fn set(&self, username: &str) {
        *self.0.lock().unwrap() = Some(username.to_owned());
    }

    fn get(&self) -> Option<String> {
        self.0.lock().unwrap().clone()
    }
}

impl<S: Send + Sync> FromRequestParts<S> for Username {
    type Rejection = Infallible;

    async fn from_request_parts(parts: &mut Parts, _state: &S) -> Result<Self, Self::Rejection> {
        Ok(parts
            .extensions
            .get::<Username>()
            .cloned()
            .unwrap_or_default())
    }
}

pub async fn action_log_middleware(
    State(action_log): State<ActionLog>,
    mut req: Request,
    next: Next,
) -> Response {
    let method = req.method().clone();
    let route = req.extensions().get::<MatchedPath>().cloned();
    let action = route
        .as_ref()
        .and_then(|path| ACTION_NAMES.get(&(method.as_str(), path.as_str())).copied());
    let ip = extract_client_ip(&req);
    // Only credential endpoints need a mutable username shared with the handler.
    let username = matches!(action, Some("login" | "register")).then(Username::default);
    if let Some(username) = &username {
        req.extensions_mut().insert(username.clone());
    }

    let response = next.run(req).await;
    let status = response.status().as_u16();

    let level = if status >= 500 {
        "error"
    } else if method == Method::GET {
        "debug"
    } else {
        "info"
    };
    if log_level_rank(level) < action_log.min_level {
        return response;
    }

    let credential_name = username.as_ref().and_then(Username::get);
    let credential_name = credential_name
        .as_deref()
        .filter(|name| USERNAME_PATTERN.is_match(name));
    let entry = ActionEntry {
        level,
        action,
        status,
        ip: &ip,
        user_id: response
            .extensions()
            .get::<Session>()
            .map(|session| session.user_id.as_str()),
        username: username.as_ref().map(|_| credential_name),
    };
    let mut line = Vec::with_capacity(192);
    if serde_json::to_writer(&mut line, &entry).is_ok() {
        line.push(b'\n');
        let _ = action_log.output.lock().unwrap().write_all(&line);
    }
    response
}
