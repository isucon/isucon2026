use axum::{
    Extension, Router,
    body::Body,
    http::{Request, StatusCode},
    routing::get,
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use bytes::Bytes;
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env, file_store::FileStore, image_api::get_image_handler,
    image_signature::image_signature, image_store::ImageStore,
    initial_post_images::InitialPostImages,
};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{str::FromStr, sync::Arc};
use tower::ServiceExt;

fn app(db: PgPool, files: FileStore, cache: Option<Arc<InitialPostImages>>) -> Router {
    let context = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: false,
        read_fence: None,
        image_store: Some(ImageStore::new(db, true, None).with_file_store(Some(files))),
    });
    let app = Router::new()
        .route("/api/images/{imageId}", get(get_image_handler))
        .with_state(context);
    if let Some(cache) = cache {
        app.layer(Extension(cache))
    } else {
        app
    }
}
async fn fetch(app: &Router, id: &str, key: &[u8], exp: i64) -> (StatusCode, String, Vec<u8>) {
    let signature = URL_SAFE_NO_PAD.encode(image_signature(key, id, exp));
    let response = app
        .clone()
        .oneshot(
            Request::get(format!("/api/images/{id}?exp={exp}&sig={signature}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let media = response
        .headers()
        .get("content-type")
        .unwrap()
        .to_str()
        .unwrap()
        .to_string();
    let bytes = response
        .into_body()
        .collect()
        .await
        .unwrap()
        .to_bytes()
        .to_vec();
    (status, media, bytes)
}

#[tokio::test]
async fn initial_image_metadata_preserves_fresh_authorization_and_falls_back_on_changes() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_initial_images_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(4)
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
      CREATE TABLE users(id text PRIMARY KEY, region text DEFAULT 'asia',status text DEFAULT 'active',secret_key_hash text DEFAULT repeat('ab',32),profile_image_id text);
      INSERT INTO users(id) VALUES('owner'),('other');
      CREATE TABLE initial_users AS SELECT id,region FROM users;
      CREATE TABLE initial_data_boundary AS SELECT '2026-10-01'::timestamptz AS created_at;
      CREATE TABLE posting_windows(id text PRIMARY KEY, local_date date,on_time_deadline timestamptz,ended_at timestamptz);
      INSERT INTO posting_windows VALUES('window','2026-09-01','2026-09-01 12:00Z',NULL);
      CREATE TABLE posts(id text PRIMARY KEY,author_id text,image_id text,posting_window_id text DEFAULT 'window',created_at timestamptz DEFAULT '2026-09-01',deleted_at timestamptz);
      CREATE TABLE comments(id text,post_id text,created_at timestamptz,deleted_at timestamptz);
      CREATE TABLE realmojis(id text,post_id text,reactor_id text,image_id text,created_at timestamptz,replaced_at timestamptz,deleted_at timestamptz);
      CREATE TABLE image_objects(id text, owner_user_id text,created_at timestamptz,data bytea DEFAULT '',media_type text DEFAULT 'image/jpeg',asset_key text,PRIMARY KEY(id,created_at)) PARTITION BY RANGE(created_at);
      CREATE TABLE image_objects_202609 PARTITION OF image_objects FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
      CREATE TABLE image_objects_other PARTITION OF image_objects DEFAULT;
    "#).execute(&db).await.unwrap();
    let root = std::env::temp_dir().join(format!("astra-initial-images-{}", uuid::Uuid::new_v4()));
    let files = FileStore::new(root.clone(), "asia".into(), Default::default(), true);
    files.prepare().await.unwrap();
    let original = include_bytes!("fixtures/small.jpg");
    let replacement = include_bytes!("fixtures/wide.jpg");
    let mut ids = Vec::new();
    for index in 0..8 {
        let id = uuid::Uuid::new_v4().to_string();
        let key = files.put(&id, &Bytes::from_static(original)).await.unwrap();
        sqlx::query("INSERT INTO posts(id,author_id,image_id) VALUES($1,'owner',$2)")
            .bind(format!("post{index}"))
            .bind(&id)
            .execute(&db)
            .await
            .unwrap();
        sqlx::query("INSERT INTO image_objects(id,owner_user_id,created_at,asset_key) VALUES($1,'owner','2026-09-01',$2)").bind(&id).bind(key).execute(&db).await.unwrap();
        ids.push(id);
    }
    // Ambiguous, non-file, noninitial and mismatched ownership rows are never cached.
    sqlx::query("UPDATE image_objects SET asset_key=NULL,data=$2 WHERE id=$1")
        .bind(&ids[1])
        .bind(original.as_slice())
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("UPDATE image_objects SET owner_user_id='other' WHERE id=$1")
        .bind(&ids[2])
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("UPDATE users SET profile_image_id=$1 WHERE id='other'")
        .bind(&ids[2])
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("INSERT INTO image_objects SELECT id,owner_user_id,created_at+interval '1 second',data,media_type,asset_key FROM image_objects WHERE id=$1").bind(&ids[3]).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO posts(id,author_id,image_id) VALUES('duplicate-post','owner',$1)")
        .bind(&ids[4])
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("UPDATE image_objects SET created_at='2026-10-02' WHERE id=$1")
        .bind(&ids[5])
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("UPDATE posts SET created_at='2026-10-02' WHERE id='post6'")
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/020-initial-history.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/021-initial-export.sql"))
        .execute(&db)
        .await
        .unwrap();
    let cache = Arc::new(InitialPostImages::load(&db, "asia").await.unwrap());
    assert!(cache.resolve(&db, &ids[0]).await.unwrap().is_some());
    for id in &ids[1..7] {
        assert!(cache.resolve(&db, id).await.unwrap().is_none());
    }
    let cached = app(db.clone(), files.clone(), Some(cache.clone()));
    let reference = app(db.clone(), files.clone(), None);
    let exp = chrono::Utc::now().timestamp() + 3600;
    let key = [0xab; 32];
    for id in &ids {
        let result = fetch(&cached, id, &key, exp).await;
        assert_eq!(result, fetch(&reference, id, &key, exp).await);
        assert_eq!(result.0, StatusCode::OK);
        assert_eq!(result.1, "image/jpeg");
        assert_eq!(result.2, original);
    }
    for (bad_key, deadline) in [([0xcd; 32], exp), (key, chrono::Utc::now().timestamp() - 1)] {
        let result = fetch(&cached, &ids[0], &bad_key, deadline).await;
        assert_eq!(result.0, StatusCode::FORBIDDEN);
        assert_eq!(result, fetch(&reference, &ids[0], &bad_key, deadline).await);
    }
    sqlx::query("UPDATE users SET secret_key_hash=repeat('cd',32) WHERE id='owner'")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        fetch(&cached, &ids[0], &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        fetch(&cached, &ids[0], &[0xcd; 32], exp).await,
        fetch(&reference, &ids[0], &[0xcd; 32], exp).await
    );
    sqlx::query(
        "UPDATE users SET secret_key_hash=repeat('ab',32),status='deleted' WHERE id='owner'",
    )
    .execute(&db)
    .await
    .unwrap();
    assert_eq!(
        fetch(&cached, &ids[0], &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("UPDATE users SET status='active' WHERE id='owner'")
        .execute(&db)
        .await
        .unwrap();
    // Each transaction sees its own invalidation; rollback restores cache eligibility.
    let changes = [
        "UPDATE posts SET deleted_at=now() WHERE id='post0'".to_string(),
        "UPDATE posts SET author_id='other' WHERE id='post0'".to_string(),
        "UPDATE posts SET image_id=gen_random_uuid()::text WHERE id='post0'".to_string(),
        "DELETE FROM posts WHERE id='post0'".to_string(),
        format!(
            "UPDATE image_objects SET media_type='image/png' WHERE id='{}'",
            ids[0]
        ),
        format!(
            "UPDATE image_objects SET owner_user_id='other' WHERE id='{}'",
            ids[0]
        ),
        format!(
            "UPDATE image_objects SET asset_key=NULL WHERE id='{}'",
            ids[0]
        ),
        format!(
            "UPDATE image_objects SET created_at='2027-01-01' WHERE id='{}'",
            ids[0]
        ),
        format!("DELETE FROM image_objects WHERE id='{}'", ids[0]),
        format!(
            "INSERT INTO image_objects SELECT id,owner_user_id,created_at+interval '1 day',data,media_type,asset_key FROM image_objects WHERE id='{}'",
            ids[0]
        ),
        "TRUNCATE image_objects_202609".to_string(),
        "TRUNCATE image_objects".to_string(),
        "TRUNCATE posts".to_string(),
        "UPDATE posting_windows SET local_date='2026-09-02'".to_string(),
    ];
    for change in changes {
        let mut tx = db.begin().await.unwrap();
        sqlx::raw_sql(&change).execute(&mut *tx).await.unwrap();
        assert!(
            cache.resolve(&mut *tx, &ids[0]).await.unwrap().is_none(),
            "{change}"
        );
        tx.rollback().await.unwrap();
        assert!(cache.resolve(&db, &ids[0]).await.unwrap().is_some());
    }
    // Reset removes only post-boundary rows with triggers disabled.
    let mut tx = db.begin().await.unwrap();
    sqlx::raw_sql("SET LOCAL session_replication_role=replica; DELETE FROM posts WHERE created_at>(SELECT created_at FROM initial_data_boundary)").execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    assert!(cache.resolve(&db, &ids[0]).await.unwrap().is_some());
    // Committed changes go through the original SQL path and cannot reuse old bytes.
    let new_key = files
        .put(
            &uuid::Uuid::new_v4().to_string(),
            &Bytes::from_static(replacement),
        )
        .await
        .unwrap();
    sqlx::query("UPDATE image_objects SET asset_key=$2 WHERE id=$1")
        .bind(&ids[0])
        .bind(new_key)
        .execute(&db)
        .await
        .unwrap();
    let result = fetch(&cached, &ids[0], &key, exp).await;
    assert_eq!(result, fetch(&reference, &ids[0], &key, exp).await);
    assert_eq!(result.2, replacement);
    sqlx::query("UPDATE posts SET deleted_at=now() WHERE id='post0'")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        fetch(&cached, &ids[0], &key, exp).await.0,
        StatusCode::FORBIDDEN
    );
    // An alternative profile reference remains valid after the post is deleted.
    sqlx::query("UPDATE users SET profile_image_id=$1 WHERE id='owner'")
        .bind(&ids[0])
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        fetch(&cached, &ids[0], &key, exp).await,
        fetch(&reference, &ids[0], &key, exp).await
    );
    assert_eq!(fetch(&cached, &ids[0], &key, exp).await.0, StatusCode::OK);
    assert!(cache.counts().0 > 0 && cache.counts().1 > 0);
    drop((cached, reference));
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    std::fs::remove_dir_all(root).unwrap();
}
