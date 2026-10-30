use std::panic::AssertUnwindSafe;
use std::time::Duration;

use axum::{Router, body::Body, http::Request};
use futures_util::FutureExt;
use http_body_util::BodyExt;
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgConnectOptions};
use tower::ServiceExt;

async fn request(app: Router, method: &str, path: &str, token: &str, body: Value) -> (u16, Value) {
    let response = app
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header("content-type", "application/json")
                .header("x-client-ip", "192.0.2.42")
                .header("x-country-code", "JP")
                .header("authorization", format!("Bearer {token}"))
                .body(Body::from(if body.is_null() {
                    String::new()
                } else {
                    body.to_string()
                }))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status().as_u16();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (
        status,
        if bytes.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&bytes).unwrap()
        },
    )
}

fn spawn_read(
    app: Router,
    path: &'static str,
    token: String,
) -> tokio::task::JoinHandle<(u16, Value)> {
    tokio::spawn(async move { request(app, "GET", path, &token, Value::Null).await })
}

async fn pause(replica: &PgPool) {
    sqlx::query("SELECT pg_wal_replay_pause()")
        .execute(replica)
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_secs(3), async {
        loop {
            let state: String = sqlx::query_scalar("SELECT pg_get_wal_replay_pause_state()")
                .fetch_one(replica)
                .await
                .unwrap();
            if state == "paused" {
                break;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn only_graph_routes_wait_for_local_replay_and_logout_stays_fresh() {
    let (Ok(primary_host), Ok(replica_host)) = (
        std::env::var("ISUREAL_SELECTIVE_PRIMARY_HOST"),
        std::env::var("ISUREAL_SELECTIVE_REPLICA_HOST"),
    ) else {
        return;
    };
    // Separate Docker IPs let both PostgreSQL endpoints use the same app port.
    let primary_options = PgConnectOptions::new()
        .host(&primary_host)
        .port(5432)
        .username("postgres")
        .database("postgres");
    let replica_options = PgConnectOptions::new()
        .host(&replica_host)
        .port(5432)
        .username("postgres")
        .database("postgres");
    let admin = PgPool::connect_with(primary_options.clone()).await.unwrap();
    let replica = PgPool::connect_with(replica_options).await.unwrap();
    let database = format!("astra_selective_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE DATABASE {database}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPool::connect_with(primary_options.database(&database))
        .await
        .unwrap();
    let result = AssertUnwindSafe(async {
        for sql in [
            include_str!("../../sql/001-schema.sql"),
            include_str!("../../sql/006-registration.sql"),
            include_str!("../../sql/014-normalized-user-search.sql"),
            include_str!("../../sql/015-search-candidates.sql"),
            include_str!("../../sql/016-notification-region-locks.sql"),
        ] {
            sqlx::raw_sql(sql).execute(&db).await.unwrap();
        }
        isureal::read_fence::ReadFence::new(admin.clone(), replica.clone(), Duration::from_secs(5))
            .wait()
            .await
            .unwrap();
        // This integration test is the only test in its executable; no other
        // server is started concurrently with these startup-only variables.
        unsafe {
            std::env::set_var("ISUREAL_DB_READ_HOST", &replica_host);
            std::env::set_var("ISUREAL_READ_FENCE", "1");
            std::env::set_var("ISUREAL_DB_READ_SCOPE", "feed_search");
        }
        let mut config = isureal::config_from_env();
        config.region = "asia".into();
        config.db_host = primary_host;
        config.db_port = "5432".into();
        config.db_user = "postgres".into();
        config.db_password = "".into();
        config.db_name = database.clone();
        let app = isureal::new_server(config).await;
        unsafe {
            std::env::remove_var("ISUREAL_DB_READ_HOST");
            std::env::remove_var("ISUREAL_READ_FENCE");
            std::env::remove_var("ISUREAL_DB_READ_SCOPE");
        }
        let (status, viewer) = request(
            app.clone(),
            "POST",
            "/api/auth/register",
            "",
            json!({"username":"viewer","displayName":"Viewer","region":"asia"}),
        )
        .await;
        assert_eq!(status, 201);
        let (status, login) = request(
            app.clone(),
            "POST",
            "/api/auth/login",
            "",
            json!({"username":"viewer","secretKey":viewer["secretKey"]}),
        )
        .await;
        assert_eq!(status, 200);
        let token = login["token"].as_str().unwrap().to_owned();
        let (status, target) = request(
            app.clone(),
            "POST",
            "/api/auth/register",
            "",
            json!({"username":"target","displayName":"Old name","region":"asia"}),
        )
        .await;
        assert_eq!(status, 201);
        assert_eq!(
            request(
                app.clone(),
                "GET",
                "/api/users?q=target",
                &token,
                Value::Null
            )
            .await
            .0,
            200
        );
        pause(&replica).await;
        sqlx::query("UPDATE users SET display_name='Fresh name' WHERE username='target'")
            .execute(&db)
            .await
            .unwrap();
        let detail = format!("/api/users/{}", target["user"]["id"].as_str().unwrap());
        let response = tokio::time::timeout(
            Duration::from_secs(1),
            request(app.clone(), "GET", &detail, &token, Value::Null),
        )
        .await
        .unwrap();
        assert_eq!(response.0, 200);
        assert_eq!(response.1["displayName"], "Fresh name");
        let search = spawn_read(app.clone(), "/api/users?q=target", token.clone());
        let feed = spawn_read(app.clone(), "/api/feeds/friends", token.clone());
        tokio::time::sleep(Duration::from_millis(200)).await;
        assert!(!search.is_finished() && !feed.is_finished());
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&replica)
            .await
            .unwrap();
        let response = search.await.unwrap();
        assert_eq!(response.0, 200);
        assert_eq!(response.1["items"][0]["displayName"], "Fresh name");
        assert_eq!(feed.await.unwrap().0, 200);
        pause(&replica).await;
        assert_eq!(
            request(app.clone(), "POST", "/api/auth/logout", &token, Value::Null)
                .await
                .0,
            204
        );
        assert_eq!(
            request(app.clone(), "GET", &detail, &token, Value::Null)
                .await
                .0,
            401
        );
        let search = spawn_read(app.clone(), "/api/users?q=target", token);
        tokio::time::sleep(Duration::from_millis(200)).await;
        assert!(!search.is_finished());
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&replica)
            .await
            .unwrap();
        assert_eq!(search.await.unwrap().0, 401);
    })
    .catch_unwind()
    .await;
    sqlx::query("SELECT pg_wal_replay_resume()")
        .execute(&replica)
        .await
        .unwrap();
    db.close().await;
    sqlx::query(&format!("DROP DATABASE {database} WITH (FORCE)"))
        .execute(&admin)
        .await
        .unwrap();
    if let Err(error) = result {
        std::panic::resume_unwind(error);
    }
}
