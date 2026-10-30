use std::{str::FromStr, sync::Arc, time::Duration};

use axum::{
    Router,
    body::Body,
    http::{Request, StatusCode},
    middleware::from_fn_with_state,
    routing::get,
};
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env,
    memory_api::{get_memories_by_date_handler, get_memories_handler},
    region::region_middleware,
    session::memory_session_middleware,
};
use serde_json::Value;
use sha2::{Digest, Sha256};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use tower::ServiceExt;

async fn request(app: &Router, uri: &str, token: &str) -> (StatusCode, Value) {
    tokio::time::timeout(Duration::from_secs(5), async {
        let response = app
            .clone()
            .oneshot(
                Request::get(uri)
                    .header("authorization", format!("Bearer {token}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let status = response.status();
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        (status, serde_json::from_slice(&bytes).unwrap())
    })
    .await
    .expect("one-connection requests must release or reuse authentication's connection")
}

#[tokio::test]
async fn memory_reuses_one_connection_and_releases_it_on_errors_with_fresh_auth() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_memory_connection_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(1)
        .acquire_timeout(Duration::from_secs(2))
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
        CREATE TABLE users (
          id text PRIMARY KEY, username text, secret_key_hash text DEFAULT repeat('ab',32),
          display_name text DEFAULT 'Test', bio text DEFAULT '', profile_image_id text,
          region text DEFAULT 'asia', account_type text DEFAULT 'regular', status text DEFAULT 'active'
        );
        CREATE TABLE login_sessions (user_id text, token_hash text, created_at timestamptz DEFAULT now(), active boolean DEFAULT true);
        CREATE TABLE posting_windows (id text PRIMARY KEY, local_date date, on_time_deadline timestamptz, ended_at timestamptz);
        CREATE TABLE posts (id text PRIMARY KEY, author_id text, posting_window_id text, image_id text,
          caption text DEFAULT '', visibility text DEFAULT 'public', location_kind text DEFAULT 'none',
          latitude numeric, longitude numeric, locality text, posted_at timestamptz, deleted_at timestamptz);
        CREATE TABLE comments (id text, post_id text, author_id text, body text, created_at timestamptz, deleted_at timestamptz);
        CREATE TABLE realmojis (id text, post_id text, reactor_id text, image_id text, reaction_type text, created_at timestamptz, replaced_at timestamptz, deleted_at timestamptz);
        INSERT INTO users(id,username) VALUES ('owner','owner'),('other','other');
        INSERT INTO posting_windows VALUES ('window','2026-10-01','2026-10-01 10:02Z','2026-10-02 10:00Z');
        INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at) VALUES ('post','owner','window','image','2026-10-01 10:00Z');
        INSERT INTO comments VALUES ('comment','post','other','fresh','2026-10-01 10:01Z',NULL);
    "#).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO login_sessions(user_id,token_hash) VALUES ('owner',$1)")
        .bind(hex::encode(Sha256::digest(b"token")))
        .execute(&db)
        .await
        .unwrap();
    let context = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: false,
        read_fence: None,
        image_store: None,
    });
    // Occupy the entire ordinary pool. The separately connected context must
    // still authenticate and read the same schema without waiting behind it.
    assert!(Arc::ptr_eq(
        &isureal::db_pool::memory_context(&context, 0).await.unwrap(),
        &context
    ));
    let isolated = isureal::db_pool::memory_context(&context, 1).await.unwrap();
    let isolated_app = Router::new()
        .route("/api/memories", get(get_memories_handler))
        .route_layer(from_fn_with_state(
            (isolated.clone(), true),
            memory_session_middleware,
        ))
        .with_state(isolated.clone());
    let held = db.acquire().await.unwrap();
    let (status, body) = request(&isolated_app, "/api/memories?month=2026-10", "token").await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["items"][0]["comments"][0]["body"], "fresh");
    assert_eq!(isolated.db.size(), 1);
    drop(held);
    isolated.db.close().await;
    let app = Router::new()
        .route("/api/memories", get(get_memories_handler))
        .route("/api/memories/{date}", get(get_memories_by_date_handler))
        .route_layer(from_fn_with_state(context.clone(), region_middleware))
        .route_layer(from_fn_with_state(
            (context.clone(), true),
            memory_session_middleware,
        ))
        .with_state(context);
    let uri = "/api/memories?month=2026-10";
    let (status, body) = request(&app, uri, "token").await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["items"][0]["id"], "post");
    assert_eq!(body["items"][0]["comments"][0]["body"], "fresh");
    let mut concurrent = tokio::task::JoinSet::new();
    for _ in 0..16 {
        let app = app.clone();
        concurrent.spawn(async move { request(&app, "/api/memories/2026-10-01", "token").await });
    }
    while let Some(result) = concurrent.join_next().await {
        assert_eq!(result.unwrap().0, StatusCode::OK);
    }
    for (uri, token, status) in [
        (uri, "wrong", StatusCode::UNAUTHORIZED),
        ("/api/memories?month=bad", "token", StatusCode::BAD_REQUEST),
        (
            "/api/memories?month=2026-10&beforePostedAt=bad",
            "token",
            StatusCode::BAD_REQUEST,
        ),
        ("/api/memories/invalid", "token", StatusCode::BAD_REQUEST),
    ] {
        assert_eq!(request(&app, uri, token).await.0, status);
    }
    for (change, status) in [
        (
            "UPDATE login_sessions SET active=false",
            StatusCode::UNAUTHORIZED,
        ),
        (
            "UPDATE login_sessions SET active=true; UPDATE users SET status='deleted' WHERE id='owner'",
            StatusCode::UNAUTHORIZED,
        ),
        (
            "UPDATE users SET status='active',region='america' WHERE id='owner'",
            StatusCode::FORBIDDEN,
        ),
        (
            "UPDATE users SET region='asia' WHERE id='owner'; UPDATE comments SET body='changed'",
            StatusCode::OK,
        ),
    ] {
        sqlx::raw_sql(change).execute(&db).await.unwrap();
        assert_eq!(request(&app, uri, "token").await.0, status);
    }
    assert_eq!(
        request(&app, uri, "token").await.1["items"][0]["comments"][0]["body"],
        "changed"
    );
    sqlx::query("ALTER TABLE comments RENAME TO hidden_comments")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(&app, uri, "token").await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    sqlx::query("ALTER TABLE hidden_comments RENAME TO comments")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(request(&app, uri, "token").await.0, StatusCode::OK);
    drop(app);
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
