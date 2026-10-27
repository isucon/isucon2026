use std::str::FromStr;
use std::sync::Arc;

use axum::{
    Router,
    body::Body,
    http::{Request, StatusCode},
    routing::get,
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::Utc;
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env, image_api::get_image_handler, image_signature::image_signature,
};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use tower::ServiceExt;

async fn image(app: Router, id: &str, key: &[u8], exp: i64) -> (StatusCode, Vec<u8>) {
    let sig = URL_SAFE_NO_PAD.encode(image_signature(key, id, exp));
    let response = app
        .oneshot(
            Request::builder()
                .uri(format!("/api/images/{id}?exp={exp}&sig={sig}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    assert_eq!(response.headers()["cache-control"], "no-store");
    if status == StatusCode::OK {
        assert_eq!(response.headers()["content-type"], "image/jpeg");
    }
    (
        status,
        response
            .into_body()
            .collect()
            .await
            .unwrap()
            .to_bytes()
            .to_vec(),
    )
}

#[tokio::test]
async fn cached_image_checks_current_content_visibility_and_credentials() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let schema = format!("astra_images_{}", uuid::Uuid::new_v4().simple());
    let admin = PgPool::connect(&dsn).await.unwrap();
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let options = PgConnectOptions::from_str(&dsn)
        .unwrap()
        .options([("search_path", schema.as_str())]);
    let db = PgPoolOptions::new()
        .max_connections(3)
        .connect_with(options)
        .await
        .unwrap();
    sqlx::raw_sql(r#"
        CREATE TABLE users (id text PRIMARY KEY, secret_key_hash text NOT NULL, status text NOT NULL DEFAULT 'active', profile_image_id text);
        CREATE TABLE image_objects (id text, owner_user_id text, data bytea NOT NULL, media_type text NOT NULL DEFAULT 'image/jpeg', created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(id,created_at)) PARTITION BY RANGE(created_at);
        CREATE TABLE image_objects_old PARTITION OF image_objects FOR VALUES FROM ('2020-01-01') TO ('2021-01-01');
        CREATE TABLE image_objects_other PARTITION OF image_objects DEFAULT;
        CREATE TABLE posts (id text PRIMARY KEY, image_id text, author_id text, deleted_at timestamptz);
        CREATE TABLE realmojis (id text PRIMARY KEY, image_id text, reactor_id text, post_id text, replaced_at timestamptz, deleted_at timestamptz);
    "#).execute(&db).await.unwrap();
    let key = [0xabu8; 32];
    let id = uuid::Uuid::new_v4().to_string();
    let original = include_bytes!("fixtures/small.jpg").as_slice();
    let changed = include_bytes!("fixtures/wide.jpg").as_slice();
    sqlx::query("INSERT INTO users (id,secret_key_hash) VALUES ('owner',$1),('author',$1)")
        .bind(hex::encode(key))
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("INSERT INTO image_objects (id,owner_user_id,data) VALUES ($1,'owner',$2)")
        .bind(&id)
        .bind(original)
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("INSERT INTO posts (id,image_id,author_id) VALUES ('post',$1,'owner')")
        .bind(&id)
        .execute(&db)
        .await
        .unwrap();
    let context = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        read_fence: None,
    });
    let app = Router::new()
        .route("/api/images/{imageId}", get(get_image_handler))
        .with_state(context);
    let exp = Utc::now().timestamp() + 60;
    for _ in 0..2 {
        let (status, body) = image(app.clone(), &id, &key, exp).await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, original);
    }
    // A row update with the same ID and creation timestamp must invalidate bytes.
    sqlx::query("UPDATE image_objects SET data=$2 WHERE id=$1")
        .bind(&id)
        .bind(changed)
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(image(app.clone(), &id, &key, exp).await.1, changed);
    // A cached row can move to another partition or be recreated by initialize.
    // The old timestamp must not hide the new row or serve its old bytes.
    sqlx::query("UPDATE image_objects SET created_at='2020-06-01',data=$2 WHERE id=$1")
        .bind(&id)
        .bind(original)
        .execute(&db)
        .await
        .unwrap();
    let (status, body) = image(app.clone(), &id, &key, exp).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, original);
    sqlx::query("DELETE FROM image_objects WHERE id=$1")
        .bind(&id)
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("INSERT INTO image_objects (id,owner_user_id,data) VALUES ($1,'owner',$2)")
        .bind(&id)
        .bind(changed)
        .execute(&db)
        .await
        .unwrap();
    let (status, body) = image(app.clone(), &id, &key, exp).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, changed);

    assert_eq!(
        image(app.clone(), &id, &[0xcdu8; 32], exp).await.0,
        StatusCode::FORBIDDEN
    );
    // Changing only the owner's key leaves the cached image version intact.
    // Authorization must still use the new key on the very next request.
    sqlx::query("UPDATE users SET secret_key_hash=$1 WHERE id='owner'")
        .bind(hex::encode([0xcdu8; 32]))
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        image(app.clone(), &id, &[0xcdu8; 32], exp).await.0,
        StatusCode::OK
    );
    sqlx::query("UPDATE users SET secret_key_hash=$1 WHERE id='owner'")
        .bind(hex::encode(key))
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, Utc::now().timestamp() - 1)
            .await
            .0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("UPDATE posts SET deleted_at=now()")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("UPDATE posts SET deleted_at=NULL")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(image(app.clone(), &id, &key, exp).await.0, StatusCode::OK);
    sqlx::query("UPDATE users SET status='deleted' WHERE id='owner'")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::raw_sql("UPDATE users SET status='active'; DELETE FROM posts;")
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("UPDATE users SET profile_image_id=$1 WHERE id='owner'")
        .bind(&id)
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(image(app.clone(), &id, &key, exp).await.0, StatusCode::OK);
    sqlx::query("UPDATE users SET profile_image_id=NULL")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("INSERT INTO posts (id,image_id,author_id) VALUES ('target','unrelated','author')")
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("INSERT INTO realmojis (id,image_id,reactor_id,post_id) VALUES ('reaction',$1,'owner','target')").bind(&id).execute(&db).await.unwrap();
    assert_eq!(image(app.clone(), &id, &key, exp).await.0, StatusCode::OK);
    sqlx::query("UPDATE realmojis SET replaced_at=now()")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::raw_sql("UPDATE realmojis SET replaced_at=NULL; UPDATE users SET status='deleted' WHERE id='author';").execute(&db).await.unwrap();
    assert_eq!(
        image(app.clone(), &id, &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::raw_sql("UPDATE users SET status='active'; UPDATE realmojis SET deleted_at=now();")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(image(app, &id, &key, exp).await.0, StatusCode::FORBIDDEN);
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
