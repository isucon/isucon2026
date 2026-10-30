use std::{collections::HashSet, str::FromStr, sync::Arc, time::Duration};

use axum::{
    Router,
    body::Body,
    http::{Request, StatusCode},
    routing::post,
};
use http_body_util::BodyExt;
use isureal::{HandlerContext, config_from_env, user_api::login_handler};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use tokio::time::timeout;
use tower::ServiceExt;

async fn login(app: Router, secret: &str) -> (StatusCode, Value) {
    let response = app
        .oneshot(
            Request::post("/login")
                .header("content-type", "application/json")
                .body(Body::from(
                    serde_json::to_vec(&json!({"username":"tester", "secretKey":secret})).unwrap(),
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (status, serde_json::from_slice(&bytes).unwrap())
}

#[tokio::test]
async fn login_replacement_stays_atomic_and_serializes_with_account_changes() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_login_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(16)
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
        CREATE TABLE users (
            id text PRIMARY KEY, username text UNIQUE, secret_key_hash text,
            display_name text DEFAULT 'Test', bio text DEFAULT '', profile_image_id text,
            region text DEFAULT 'asia', account_type text DEFAULT 'regular', status text DEFAULT 'active'
        );
        CREATE TABLE login_sessions (
            id text PRIMARY KEY, user_id text REFERENCES users(id), token_hash text UNIQUE,
            created_at timestamptz DEFAULT clock_timestamp(), active boolean DEFAULT true,
            invalidated_at timestamptz, reason text
        );
    "#).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO users(id,username,secret_key_hash) VALUES ('user','tester',$1)")
        .bind(hex::encode(Sha256::digest(b"valid")))
        .execute(&db)
        .await
        .unwrap();
    let app = Router::new()
        .route("/login", post(login_handler))
        .with_state(Arc::new(HandlerContext {
            session_lookups: Default::default(),
            db: db.clone(),
            config: config_from_env(),
            ip_bans: Default::default(),
            notifications: Default::default(),
            notification_batch_writes: false,
            read_fence: None,
            image_store: None,
        }));
    let mut requests = tokio::task::JoinSet::new();
    for _ in 0..12 {
        let app = app.clone();
        requests.spawn(async move { login(app, "valid").await });
    }
    let mut tokens = HashSet::new();
    while let Some(response) = requests.join_next().await {
        let (status, body) = response.unwrap();
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["user"]["id"], "user");
        tokens.insert(body["token"].as_str().unwrap().to_owned());
    }
    assert_eq!(tokens.len(), 12);
    let active: Vec<String> =
        sqlx::query_scalar("SELECT token_hash FROM login_sessions WHERE active")
            .fetch_all(&db)
            .await
            .unwrap();
    assert_eq!(active.len(), 1);
    assert!(
        tokens
            .iter()
            .any(|token| hex::encode(Sha256::digest(token.as_bytes())) == active[0])
    );
    let replaced: i64=sqlx::query_scalar("SELECT count(*) FROM login_sessions WHERE NOT active AND reason='replaced' AND invalidated_at IS NOT NULL").fetch_one(&db).await.unwrap();
    assert_eq!(replaced, 11);
    assert_eq!(
        login(app.clone(), "wrong").await.0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT token_hash FROM login_sessions WHERE active")
            .fetch_one(&db)
            .await
            .unwrap(),
        active[0]
    );
    // Inserting a new session can fail after invalidation. The old session must survive rollback.
    sqlx::raw_sql("CREATE FUNCTION reject_login() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced session failure'; END $$; CREATE TRIGGER reject_login BEFORE INSERT ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_login();").execute(&db).await.unwrap();
    assert_eq!(
        login(app.clone(), "valid").await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT token_hash FROM login_sessions WHERE active")
            .fetch_one(&db)
            .await
            .unwrap(),
        active[0]
    );
    sqlx::query("DROP TRIGGER reject_login ON login_sessions")
        .execute(&db)
        .await
        .unwrap();
    // The credential/status read waits for an account transaction and then checks its committed value.
    let mut deletion = db.begin().await.unwrap();
    sqlx::query("UPDATE users SET status='deleted' WHERE id='user'")
        .execute(&mut *deletion)
        .await
        .unwrap();
    let cloned = app.clone();
    let mut waiting = tokio::spawn(async move { login(cloned, "valid").await });
    assert!(
        timeout(Duration::from_millis(100), &mut waiting)
            .await
            .is_err()
    );
    deletion.commit().await.unwrap();
    assert_eq!(
        timeout(Duration::from_secs(2), waiting)
            .await
            .unwrap()
            .unwrap()
            .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM login_sessions")
            .fetch_one(&db)
            .await
            .unwrap(),
        12
    );
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
