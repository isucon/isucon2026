use std::str::FromStr;
use std::sync::Arc;
use std::time::Duration;

use axum::{
    Router,
    body::Body,
    http::{Request, StatusCode},
    routing::post,
};
use http_body_util::BodyExt;
use isureal::{HandlerContext, config_from_env, user_api::register_handler};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use tokio::time::timeout;
use tower::ServiceExt;

async fn register(app: Router, username: &str) -> (StatusCode, Value) {
    let response = app.oneshot(Request::builder().method("POST").uri("/register")
        .header("content-type", "application/json")
        .body(Body::from(serde_json::to_vec(&json!({"username": username, "displayName": "Test User", "region": "europe"})).unwrap())).unwrap())
        .await.unwrap();
    let status = response.status();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (status, serde_json::from_slice(&bytes).unwrap())
}

#[tokio::test]
async fn registration_is_unique_and_independent_users_do_not_serialize() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let schema = format!("astra_register_{}", uuid::Uuid::new_v4().simple());
    let admin = PgPool::connect(&dsn).await.unwrap();
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let options = PgConnectOptions::from_str(&dsn)
        .unwrap()
        .options([("search_path", schema.as_str())]);
    let db = PgPoolOptions::new()
        .max_connections(16)
        .connect_with(options)
        .await
        .unwrap();
    sqlx::raw_sql(
        r#"
        CREATE TABLE users (
            id varchar(36) PRIMARY KEY, username varchar(64) NOT NULL,
            secret_key_hash text NOT NULL, display_name text NOT NULL,
            bio text NOT NULL DEFAULT '', profile_image_id varchar(36), region text NOT NULL,
            account_type text NOT NULL DEFAULT 'regular', status text NOT NULL DEFAULT 'active'
        );
    "#,
    )
    .execute(&db)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!("../../sql/006-registration.sql"))
        .execute(&db)
        .await
        .unwrap();
    let context = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        read_fence: None,
        image_store: None,
    });
    let app = Router::new()
        .route("/register", post(register_handler))
        .with_state(context);
    let mut concurrent = tokio::task::JoinSet::new();
    for _ in 0..12 {
        let app = app.clone();
        concurrent.spawn(async move { register(app, "same_user").await });
    }
    let mut created = Vec::new();
    let mut conflicts = 0;
    while let Some(result) = concurrent.join_next().await {
        let (status, body) = result.unwrap();
        match status {
            StatusCode::CREATED => created.push(body),
            StatusCode::CONFLICT => {
                conflicts += 1;
                assert_eq!(body["error"], "username_taken");
            }
            status => panic!("unexpected registration status: {status}: {body}"),
        }
    }
    assert_eq!(created.len(), 1);
    assert_eq!(conflicts, 11);
    let body = &created[0];
    assert_eq!(body["user"]["username"], "same_user");
    assert_eq!(body["user"]["region"], "europe");
    assert_eq!(body["user"]["status"], "active");
    let stored: String =
        sqlx::query_scalar("SELECT secret_key_hash FROM users WHERE username='same_user'")
            .fetch_one(&db)
            .await
            .unwrap();
    assert_eq!(
        stored,
        hex::encode(Sha256::digest(
            body["secretKey"].as_str().unwrap().as_bytes()
        ))
    );
    // Holding an unrelated insert open must not block new registrations.
    let mut held = db.begin().await.unwrap();
    sqlx::query("INSERT INTO users (id,username,secret_key_hash,display_name,region) VALUES ('held','held','unused','held','asia')").execute(&mut *held).await.unwrap();
    let (status, _) = timeout(Duration::from_secs(2), register(app.clone(), "independent"))
        .await
        .unwrap();
    assert_eq!(status, StatusCode::CREATED);
    held.rollback().await.unwrap();
    // Posting-window creation still serializes with registration through its
    // existing table lock; it observes each new user wholly before or after.
    let mut window = db.begin().await.unwrap();
    sqlx::query("LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE")
        .execute(&mut *window)
        .await
        .unwrap();
    let cloned = app.clone();
    let mut waiting = tokio::spawn(async move { register(cloned, "after_window").await });
    assert!(
        timeout(Duration::from_millis(150), &mut waiting)
            .await
            .is_err()
    );
    window.commit().await.unwrap();
    assert_eq!(
        timeout(Duration::from_secs(2), waiting)
            .await
            .unwrap()
            .unwrap()
            .0,
        StatusCode::CREATED
    );
    assert_eq!(
        register(app, "INVALID USER").await.0,
        StatusCode::BAD_REQUEST
    );
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
