use std::panic::AssertUnwindSafe;
use std::time::Duration;

use axum::{Router, body::Body, http::Request};
use futures_util::FutureExt;
use http_body_util::BodyExt;
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgConnectOptions};
use tokio::time::{sleep, timeout};
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

async fn configure_standbys(admin: &PgPool, names: &str, count: i64) {
    sqlx::query(&format!(
        "ALTER SYSTEM SET synchronous_standby_names = '{names}'"
    ))
    .execute(admin)
    .await
    .unwrap();
    sqlx::query("SELECT pg_reload_conf()")
        .execute(admin)
        .await
        .unwrap();
    timeout(Duration::from_secs(5), async {
        while sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM pg_stat_replication WHERE sync_state='sync'",
        )
        .fetch_one(admin)
        .await
        .unwrap()
            != count
        {
            sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn asia_local_reads_require_third_acknowledgement_and_observe_logout() {
    let (Ok(primary_host), Ok(third_host)) = (
        std::env::var("ISUREAL_SELECTIVE_PRIMARY_HOST"),
        std::env::var("ISUREAL_THIRD_REPLICA_HOST"),
    ) else {
        return;
    };
    let options = PgConnectOptions::new()
        .host(&primary_host)
        .port(5432)
        .username("postgres")
        .database("postgres")
        .options([("synchronous_commit", "local")]);
    let admin = PgPool::connect_with(options.clone()).await.unwrap();
    let third = PgPool::connect_with(
        PgConnectOptions::new()
            .host(&third_host)
            .port(5432)
            .username("postgres")
            .database("postgres"),
    )
    .await
    .unwrap();
    let previous: String = sqlx::query_scalar("SHOW synchronous_standby_names")
        .fetch_one(&admin)
        .await
        .unwrap();
    assert_eq!(previous, "", "requires an asynchronous test primary");
    let database = format!("astra_three_reads_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE DATABASE {database}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPool::connect_with(options.database(&database))
        .await
        .unwrap();
    let result = AssertUnwindSafe(async {
        for sql in [
            include_str!("../../sql/001-schema.sql"),
            include_str!("../../sql/006-registration.sql"),
            include_str!("../../sql/014-normalized-user-search.sql"),
            include_str!("../../sql/015-search-candidates.sql"),
            include_str!("../../sql/016-notification-region-locks.sql"),
            "CREATE TABLE astra_visibility_barriers (id text PRIMARY KEY)",
        ] {
            sqlx::raw_sql(sql).execute(&db).await.unwrap();
        }
        isureal::read_fence::ReadFence::new(admin.clone(), third.clone(), Duration::from_secs(5))
            .wait()
            .await
            .unwrap();
        // This executable starts no other server alongside these startup-only variables.
        unsafe {
            std::env::set_var("ISUREAL_DB_READ_HOST", &third_host);
            std::env::set_var("ISUREAL_READ_FENCE", "0");
            std::env::set_var("ISUREAL_DB_READ_SCOPE", "all");
            std::env::set_var("ISUREAL_WRITE_FENCE_HOSTS", "172.17.0.3,172.17.0.4");
            std::env::set_var("ISUREAL_WRITE_FENCE_MODE", "native");
        }
        let mut config = isureal::config_from_env();
        config.region = "asia".into();
        config.db_host = primary_host;
        config.db_port = "5432".into();
        config.db_user = "postgres".into();
        config.db_password = "".into();
        config.db_name = database.clone();
        configure_standbys(&admin, "FIRST 2 (astra_europe, astra_america)", 2).await;
        assert!(
            AssertUnwindSafe(isureal::new_server(config.clone()))
                .catch_unwind()
                .await
                .is_err(),
            "Asia reads without a read fence must reject a two-standby acknowledgement set"
        );
        configure_standbys(
            &admin,
            "FIRST 3 (astra_europe, astra_america, astra_asia)",
            3,
        )
        .await;
        let app = isureal::new_server(config).await;
        let (status, user) = request(
            app.clone(),
            "POST",
            "/api/auth/register",
            "",
            json!({"username":"viewer", "displayName":"Viewer", "region":"asia"}),
        )
        .await;
        assert_eq!(status, 201);
        let (status, login) = request(
            app.clone(),
            "POST",
            "/api/auth/login",
            "",
            json!({"username":"viewer", "secretKey":user["secretKey"]}),
        )
        .await;
        assert_eq!(status, 200);
        let token = login["token"].as_str().unwrap().to_owned();
        assert_eq!(
            request(app.clone(), "GET", "/api/me", &token, Value::Null)
                .await
                .0,
            200
        );
        sqlx::query("SELECT pg_wal_replay_pause()")
            .execute(&third)
            .await
            .unwrap();
        timeout(Duration::from_secs(5), async {
            while sqlx::query_scalar::<_, String>("SELECT pg_get_wal_replay_pause_state()")
                .fetch_one(&third)
                .await
                .unwrap()
                != "paused"
            {
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        // Generate newer WAL without changing the already-visible session.
        sqlx::query("INSERT INTO astra_visibility_barriers VALUES ('local-read-probe')")
            .execute(&db)
            .await
            .unwrap();
        assert_eq!(
            timeout(
                Duration::from_secs(1),
                request(app.clone(), "GET", "/api/me", &token, Value::Null)
            )
            .await
            .unwrap()
            .0,
            200,
            "local reads must not wait for a fresh primary WAL position"
        );
        let logout_app = app.clone();
        let logout_token = token.clone();
        let logout = tokio::spawn(async move {
            request(
                logout_app,
                "POST",
                "/api/auth/logout",
                &logout_token,
                Value::Null,
            )
            .await
        });
        sleep(Duration::from_millis(200)).await;
        assert!(!logout.is_finished());
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&third)
            .await
            .unwrap();
        assert_eq!(
            timeout(Duration::from_secs(5), logout)
                .await
                .unwrap()
                .unwrap()
                .0,
            204
        );
        assert_eq!(
            request(app, "GET", "/api/me", &token, Value::Null).await.0,
            401
        );
    })
    .catch_unwind()
    .await;
    unsafe {
        for name in [
            "ISUREAL_DB_READ_HOST",
            "ISUREAL_READ_FENCE",
            "ISUREAL_DB_READ_SCOPE",
            "ISUREAL_WRITE_FENCE_HOSTS",
            "ISUREAL_WRITE_FENCE_MODE",
        ] {
            std::env::remove_var(name);
        }
    }
    sqlx::query("SELECT pg_wal_replay_resume()")
        .execute(&third)
        .await
        .unwrap();
    configure_standbys(&admin, "", 0).await;
    db.close().await;
    sqlx::query(&format!("DROP DATABASE {database} WITH (FORCE)"))
        .execute(&admin)
        .await
        .unwrap();
    if let Err(panic) = result {
        std::panic::resume_unwind(panic);
    }
}
