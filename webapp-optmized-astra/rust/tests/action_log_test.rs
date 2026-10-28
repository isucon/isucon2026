use std::io::Write;
use std::sync::{Arc, Mutex};

use axum::Router;
use axum::body::Body;
use axum::http::{Request, StatusCode};
use axum::middleware::from_fn_with_state;
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use chrono::Utc;
use isureal::Error;
use isureal::action_log::{ActionLog, Username, action_log_middleware};
use isureal::api_error::HttpError;
use isureal::bind::Bind;
use isureal::new_api_error;
use isureal::session::Session;
use isureal::user_api::LoginRequest;
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Clone, Default)]
struct Output(Arc<Mutex<Vec<u8>>>);

impl Write for Output {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

impl Output {
    fn lines(&self) -> Vec<Value> {
        let data = String::from_utf8(self.0.lock().unwrap().clone()).unwrap();
        data.lines()
            .filter(|line| !line.is_empty())
            .map(|line| serde_json::from_str(line).unwrap())
            .collect()
    }
}

fn session() -> Session {
    Session {
        region: "asia".into(),
        token: String::new(),
        user_id: "user-1".into(),
        token_hash: String::new(),
        created_at: Utc::now(),
    }
}

fn new_server(min_level: &str) -> (Router, Output) {
    let output = Output::default();
    let app = Router::new()
        .route(
            "/api/auth/login",
            post(
                |username: Username, Bind(input): Bind<LoginRequest>| async move {
                    username.set(&input.username);
                    new_api_error(StatusCode::UNAUTHORIZED, "invalid_credentials")
                },
            ),
        )
        .route(
            "/api/posts",
            post(|| async {
                let mut response = StatusCode::CREATED.into_response();
                response.extensions_mut().insert(session());
                response
            }),
        )
        .route(
            "/api/feeds/{feed}",
            get(|| async {
                let mut response: Response = StatusCode::OK.into_response();
                response.extensions_mut().insert(session());
                response
            }),
        )
        .route(
            "/api/users/{userId}",
            get(|| async { Error::Internal(anyhow::anyhow!("boom")) }),
        )
        .fallback(|| async { Error::Http(HttpError::new(StatusCode::NOT_FOUND)) })
        .layer(from_fn_with_state(
            ActionLog::new(min_level, output.clone()),
            action_log_middleware,
        ));
    (app, output)
}

async fn send(app: &Router, method: &str, path: &str, body: &str) {
    let request = Request::builder()
        .method(method)
        .uri(path)
        .header("Content-Type", "application/json")
        .header("X-Client-IP", "203.0.113.7")
        .body(Body::from(body.to_owned()))
        .unwrap();
    app.clone().oneshot(request).await.unwrap();
}

#[tokio::test]
async fn action_log_middleware_entries() {
    let (app, output) = new_server("debug");
    send(
        &app,
        "POST",
        "/api/auth/login",
        r#"{"username":"alice","secretKey":"wrong"}"#,
    )
    .await;
    send(
        &app,
        "POST",
        "/api/auth/login",
        r#"{"username":"Not Valid","secretKey":"wrong"}"#,
    )
    .await;
    send(&app, "POST", "/api/posts", "{}").await;
    send(&app, "GET", "/api/feeds/friends", "").await;
    send(&app, "GET", "/api/users/someone", "").await;
    send(&app, "GET", "/api/unknown", "").await;
    let want = vec![
        json!({"level": "info", "action": "login", "status": 401, "ip": "203.0.113.7", "user_id": null, "username": "alice"}),
        json!({"level": "info", "action": "login", "status": 401, "ip": "203.0.113.7", "user_id": null, "username": null}),
        json!({"level": "info", "action": "create_post", "status": 201, "ip": "203.0.113.7", "user_id": "user-1"}),
        json!({"level": "debug", "action": "get_feed", "status": 200, "ip": "203.0.113.7", "user_id": "user-1"}),
        json!({"level": "error", "action": "get_user", "status": 500, "ip": "203.0.113.7", "user_id": null}),
        json!({"level": "debug", "action": null, "status": 404, "ip": "203.0.113.7", "user_id": null}),
    ];
    assert_eq!(output.lines(), want);

    // info では読み取り（GET）の行を出さない。
    let (app, output) = new_server("info");
    send(&app, "GET", "/api/feeds/friends", "").await;
    send(&app, "POST", "/api/posts", "{}").await;
    let lines = output.lines();
    assert_eq!(lines.len(), 1, "{lines:?}");
    assert_eq!(lines[0]["action"], "create_post");
    send(
        &app,
        "POST",
        "/api/auth/login",
        r#"{"username":"alice","secretKey":"wrong"}"#,
    )
    .await;
    let lines = output.lines();
    assert_eq!(lines.len(), 2);
    assert_eq!(lines[1]["action"], "login");
    assert_eq!(lines[1]["status"], 401);
    assert_eq!(lines[1]["ip"], "203.0.113.7");
}

#[tokio::test]
async fn error_level_retains_failed_logins_for_guard_and_server_errors() {
    let (app, output) = new_server("error");
    send(&app, "POST", "/api/posts", "{}").await;
    send(&app, "GET", "/api/feeds/friends", "").await;
    assert!(output.lines().is_empty());
    for username in ["alice", "Not Valid"] {
        let body = json!({"username": username, "secretKey": "wrong"}).to_string();
        send(&app, "POST", "/api/auth/login", &body).await;
    }
    send(&app, "GET", "/api/users/someone", "").await;
    assert_eq!(
        output.lines(),
        vec![
            json!({"level": "info", "action": "login", "status": 401, "ip": "203.0.113.7", "user_id": null, "username": "alice"}),
            json!({"level": "info", "action": "login", "status": 401, "ip": "203.0.113.7", "user_id": null, "username": null}),
            json!({"level": "error", "action": "get_user", "status": 500, "ip": "203.0.113.7", "user_id": null}),
        ]
    );
}
