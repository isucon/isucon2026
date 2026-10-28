use std::env;
use std::sync::Arc;

use axum::Router;
use axum::body::Body;
use axum::http::{Request, StatusCode, header};
use axum::middleware::from_fn_with_state;
use axum::routing::get;
use chrono::{TimeZone, Utc};
use http_body_util::BodyExt;
use isureal::feed_api::get_feed_handler;
use isureal::ip_ban::IpBanList;
use isureal::session::{create_login_session, session_middleware};
use isureal::{HandlerContext, config_from_env};
use percent_encoding::{NON_ALPHANUMERIC, utf8_percent_encode};
use serde_json::Value;
use sqlx::PgPool;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

async fn request(app: &Router, token: &str, path: &str, status: StatusCode) -> Value {
    let request = Request::get(path)
        .header(header::AUTHORIZATION, format!("Bearer {token}"))
        .body(Body::empty())
        .unwrap();
    let response = app.clone().oneshot(request).await.unwrap();
    let got = response.status();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(
        got,
        status,
        "{path} body={}",
        String::from_utf8_lossy(&body)
    );
    serde_json::from_slice(&body).unwrap()
}

async fn assert_ranking(app: &Router, token: &str, path: &str, expected: &[&str]) -> Value {
    let response = request(app, token, path, StatusCode::OK).await;
    let got: Vec<String> = response["items"]
        .as_array()
        .unwrap()
        .iter()
        .map(|item| {
            let score = item["score"]
                .as_i64()
                .unwrap_or_else(|| panic!("{path}: post {} has no score", item["id"]));
            format!("{}:{score}", item["id"].as_str().unwrap())
        })
        .collect();
    assert_eq!(got, expected, "{path}");
    response
}

async fn insert_engagement(db: &PgPool, query: &str, count: usize) {
    let mut query = sqlx::query(query);
    for _ in 0..count {
        query = query.bind(uuid::Uuid::new_v4().to_string());
    }
    query.execute(db).await.unwrap();
}

#[tokio::test]
async fn recommended_feed_integration() {
    let Ok(dsn) = env::var("ISUREAL_SESSION_TEST_DSN") else {
        eprintln!("ISUREAL_SESSION_TEST_DSN is unset");
        return;
    };
    let db = PgPoolOptions::new().connect(&dsn).await.unwrap();
    let schema = std::fs::read_to_string("../sql/001-schema.sql").unwrap();
    sqlx::raw_sql(&schema).execute(&db).await.unwrap();
    for id in ["viewer", "f1", "f2", "a1", "a2", "x"] {
        sqlx::query(
            "INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, '9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6', $3, 'asia')",
        )
        .bind(id)
        .bind(id)
        .bind(id)
        .execute(&db)
        .await
        .unwrap();
    }
    let session = create_login_session(&db, "viewer").await.unwrap();
    sqlx::query("INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)")
        .execute(&db)
        .await
        .unwrap();
    for (i, author) in ["viewer", "a1", "a2", "x", "f1"].into_iter().enumerate() {
        sqlx::query("INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, $3, 'image/jpeg')")
            .bind(format!("{author}-image"))
            .bind(author)
            .bind(b"image".to_vec())
            .execute(&db)
            .await
            .unwrap();
        sqlx::query("INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at) VALUES ($1, $2, 'w', $3, 'caption', 'friends_of_friends', 'none', $4)")
            .bind(author)
            .bind(author)
            .bind(format!("{author}-image"))
            .bind(Utc.with_ymd_and_hms(2026, 9, 23, 0, 1, i as u32).unwrap())
            .execute(&db)
            .await
            .unwrap();
    }
    for (a, b) in [
        ("f1", "viewer"),
        ("f2", "viewer"),
        ("a1", "f1"),
        ("a1", "f2"),
        ("a2", "f1"),
        ("a1", "x"),
        ("a2", "x"),
        ("f2", "x"),
    ] {
        sqlx::query("INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, $2, $3)")
            .bind(uuid::Uuid::new_v4().to_string())
            .bind(a.min(b))
            .bind(a.max(b))
            .execute(&db)
            .await
            .unwrap();
    }
    let h = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: IpBanList::default(),
    });
    let app = Router::new()
        .route(
            "/api/feeds/{feed}",
            get(get_feed_handler)
                .route_layer(from_fn_with_state((h.clone(), true), session_middleware)),
        )
        .with_state(h);
    let token = session.token.as_str();

    let feed = "/api/feeds/friends-of-friends";
    assert_ranking(&app, token, feed, &["a1:7", "x:6", "a2:4"]).await;
    insert_engagement(&db, "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a2', 'f1', 'rm', 'smile', clock_timestamp())", 1).await;
    insert_engagement(&db, "INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES ($1, 'a2', 'f1', 'nice', clock_timestamp()), ($2, 'a2', 'x', 'nice', clock_timestamp())", 2).await;
    let page = assert_ranking(&app, token, feed, &["a2:14", "a1:7", "x:6"]).await;
    insert_engagement(&db, "INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a1', 'f2', 'rm', 'smile', clock_timestamp())", 1).await;
    sqlx::query("UPDATE users SET status='deleted' WHERE id='f2'")
        .execute(&db)
        .await
        .unwrap();
    assert_ranking(&app, token, feed, &["a2:14", "a1:7", "x:6"]).await;

    let cursor = &page["items"][1];
    let before = [
        ("beforeScore", cursor["score"].to_string()),
        (
            "beforePostedAt",
            cursor["postedAt"].as_str().unwrap().to_owned(),
        ),
        ("beforeId", cursor["id"].as_str().unwrap().to_owned()),
    ]
    .iter()
    .map(|(key, value)| format!("{key}={}", utf8_percent_encode(value, NON_ALPHANUMERIC)))
    .collect::<Vec<_>>()
    .join("&");
    assert_ranking(&app, token, &format!("{feed}?{before}"), &["x:6"]).await;
    request(
        &app,
        token,
        &format!("{feed}?beforeScore=7"),
        StatusCode::BAD_REQUEST,
    )
    .await;
    request(
        &app,
        token,
        &format!("{feed}?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1"),
        StatusCode::BAD_REQUEST,
    )
    .await;

    let friends = request(&app, token, "/api/feeds/friends", StatusCode::OK).await;
    let items = friends["items"].as_array().unwrap();
    assert_eq!(items.len(), 1, "friends feed: {friends}");
    assert_eq!(items[0]["id"], "f1");
    assert!(
        items[0].get("score").is_none(),
        "friends feed returned a score for f1"
    );
    request(
        &app,
        token,
        "/api/feeds/friends?beforeScore=abc",
        StatusCode::BAD_REQUEST,
    )
    .await;
}
