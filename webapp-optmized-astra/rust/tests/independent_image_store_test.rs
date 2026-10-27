use axum::{
    Extension, Router,
    body::Body,
    http::{Request, StatusCode},
    routing::{get, post},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::Utc;
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env, export_api::export_posts_handler,
    image_api::get_image_handler, image_signature::image_signature, image_store::ImageStore,
    post_api::create_post_handler, session::Session,
};
use serde_json::Value;
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{
    io::{Cursor, Read},
    str::FromStr,
    sync::Arc,
    time::Duration,
};
use tower::ServiceExt;

async fn request(app: Router, request: Request<Body>) -> (StatusCode, Vec<u8>) {
    let response = app.oneshot(request).await.unwrap();
    let status = response.status();
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
fn upload(data: &[u8]) -> Request<Body> {
    let mut body = Vec::new();
    for (name, value) in [
        ("caption", "image store test"),
        ("visibility", "public"),
        ("location_kind", "none"),
    ] {
        body.extend_from_slice(
            format!(
                "--boundary\r\nContent-Disposition: form-data; name=\"{name}\"\r\n\r\n{value}\r\n"
            )
            .as_bytes(),
        );
    }
    body.extend_from_slice(b"--boundary\r\nContent-Disposition: form-data; name=\"image\"; filename=\"image.jpg\"\r\nContent-Type: image/jpeg\r\n\r\n");
    body.extend_from_slice(data);
    body.extend_from_slice(b"\r\n--boundary--\r\n");
    Request::post("/api/posts")
        .header("content-type", "multipart/form-data; boundary=boundary")
        .body(Body::from(body))
        .unwrap()
}
fn image_url(id: &str, key: &[u8]) -> String {
    let exp = Utc::now().timestamp() + 3600;
    format!(
        "/api/images/{id}?exp={exp}&sig={}",
        URL_SAFE_NO_PAD.encode(image_signature(key, id, exp))
    )
}
fn app(db: PgPool, store: Option<ImageStore>) -> Router {
    let h = Arc::new(HandlerContext {
        db,
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        read_fence: None,
        image_store: store,
    });
    Router::new()
        .route("/api/posts", post(create_post_handler))
        .route("/api/images/{imageId}", get(get_image_handler))
        .route("/api/me/export", get(export_posts_handler))
        .layer(Extension(Session {
            user_id: "owner".into(),
            region: "asia".into(),
            token: String::new(),
            token_hash: String::new(),
            created_at: Utc::now(),
        }))
        .with_state(h)
}
async fn pool(dsn: &str, schema: &str) -> PgPool {
    PgPoolOptions::new()
        .max_connections(8)
        .connect_with(
            PgConnectOptions::from_str(dsn)
                .unwrap()
                .options([("search_path", schema)]),
        )
        .await
        .unwrap()
}
async fn count(db: &PgPool, table: &str) -> i64 {
    sqlx::query_scalar(&format!("SELECT count(*) FROM {table}"))
        .fetch_one(db)
        .await
        .unwrap()
}
async fn next_window(db: &PgPool, day: i32) {
    sqlx::query("UPDATE posting_windows SET ended_at=clock_timestamp() WHERE ended_at IS NULL")
        .execute(db)
        .await
        .unwrap();
    sqlx::query("INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES ($1,'asia',current_date+$2,clock_timestamp()-interval '1 second',clock_timestamp()+interval '1 hour')")
        .bind(format!("window-{day}")).bind(day).execute(db).await.unwrap();
}

#[tokio::test]
async fn independent_bytes_publish_atomically_and_preserve_visibility_export_and_reset() {
    let (Ok(dsn), Ok(asset_dsn)) = (
        std::env::var("ISUREAL_SESSION_TEST_DSN"),
        std::env::var("ISUREAL_ASSET_TEST_DSN"),
    ) else {
        return;
    };
    let schema = format!("astra_asset_{}", uuid::Uuid::new_v4().simple());
    let admin = PgPool::connect(&dsn).await.unwrap();
    let asset_admin = PgPool::connect(&asset_dsn).await.unwrap();
    let primary_id: i64 =
        sqlx::query_scalar("SELECT system_identifier::bigint FROM pg_control_system()")
            .fetch_one(&admin)
            .await
            .unwrap();
    let asset_id: i64 =
        sqlx::query_scalar("SELECT system_identifier::bigint FROM pg_control_system()")
            .fetch_one(&asset_admin)
            .await
            .unwrap();
    assert_ne!(
        primary_id, asset_id,
        "test requires independent PostgreSQL clusters"
    );
    for db in [&admin, &asset_admin] {
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(db)
            .await
            .unwrap();
    }
    let db = pool(&dsn, &schema).await;
    let assets = pool(&asset_dsn, &schema).await;
    sqlx::raw_sql(include_str!("../../sql/001-schema.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/005-create-post.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE astra_image_data(id varchar(36) PRIMARY KEY,data bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp()); ALTER TABLE astra_image_data ALTER COLUMN data SET STORAGE EXTERNAL")
        .execute(&assets).await.unwrap();
    let original = include_bytes!("fixtures/small.jpg").as_slice();
    let second = include_bytes!("fixtures/wide.jpg").as_slice();
    let key = [0xabu8; 32];
    sqlx::query("INSERT INTO users(id,username,secret_key_hash,display_name,region,created_at,updated_at) VALUES ('owner','owner',$1,'Owner','asia','2025-01-01','2025-01-01')")
        .bind(hex::encode(key)).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO image_objects(id,owner_user_id,data,media_type,created_at) VALUES ('legacy','owner',$1,'image/jpeg','2025-01-01')")
        .bind(original).execute(&db).await.unwrap();
    sqlx::raw_sql("INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline,ended_at,created_at) VALUES ('legacy','asia','2025-01-01','2025-01-01','2025-01-02','2025-01-02','2025-01-01'); INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at,created_at) VALUES ('legacy','owner','legacy','legacy','public','none','2025-01-01','2025-01-01')")
        .execute(&db).await.unwrap();
    sqlx::raw_sql(include_str!("../../sql/003-initial-snapshot.sql"))
        .execute(&db)
        .await
        .unwrap();
    next_window(&db, 0).await;
    let store = ImageStore::new(assets.clone(), true, None);
    let routed = app(db.clone(), Some(store.clone()));

    // Hold metadata publication. Durable bytes alone must not expose an image.
    let mut lock = db.begin().await.unwrap();
    sqlx::query("LOCK TABLE image_objects IN SHARE MODE")
        .execute(&mut *lock)
        .await
        .unwrap();
    let pending = tokio::spawn(request(routed.clone(), upload(original)));
    tokio::time::timeout(Duration::from_secs(3), async {
        while count(&assets, "astra_image_data").await != 1 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    let external_id: String = sqlx::query_scalar("SELECT id FROM astra_image_data")
        .fetch_one(&assets)
        .await
        .unwrap();
    assert_eq!(count(&db, "image_objects").await, 1);
    let external_url = image_url(&external_id, &key);
    assert_eq!(
        request(
            routed.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(store.get(&external_id).await.unwrap(), original);
    lock.commit().await.unwrap();
    let (status, response) = pending.await.unwrap();
    assert_eq!(
        status,
        StatusCode::CREATED,
        "{}",
        String::from_utf8_lossy(&response)
    );
    let response: Value = serde_json::from_slice(&response).unwrap();
    let post_id = response["id"].as_str().unwrap().to_owned();
    let metadata: (i32, Option<String>) =
        sqlx::query_as("SELECT octet_length(data),asset_key FROM image_objects WHERE id=$1")
            .bind(&external_id)
            .fetch_one(&db)
            .await
            .unwrap();
    assert_eq!(metadata, (0, Some(external_id.clone())));
    // A trusted cache delivery can satisfy the first GET without opening an
    // asset DB connection, while an unpublished or poisoned entry cannot leak.
    let created: chrono::DateTime<Utc> =
        sqlx::query_scalar("SELECT created_at FROM image_objects WHERE id=$1")
            .bind(&external_id)
            .fetch_one(&db)
            .await
            .unwrap();
    let receiver = isureal::image_cache_push::receiver_router("192.168.145.10".parse().unwrap());
    let cache_path = format!(
        "/internal/astra/image-cache/{external_id}?createdAtMicros={}",
        created.timestamp_micros()
    );
    let mut delivery = Request::post(&cache_path)
        .body(Body::from(original.to_vec()))
        .unwrap();
    delivery.extensions_mut().insert(axum::extract::ConnectInfo(
        "192.168.145.10:1234"
            .parse::<std::net::SocketAddr>()
            .unwrap(),
    ));
    assert_eq!(
        request(receiver.clone(), delivery).await.0,
        StatusCode::NO_CONTENT
    );
    let mut poison = Request::post(&cache_path)
        .header("X-Forwarded-For", "192.168.145.10")
        .body(Body::from(second.to_vec()))
        .unwrap();
    poison.extensions_mut().insert(axum::extract::ConnectInfo(
        "192.168.146.10:1234"
            .parse::<std::net::SocketAddr>()
            .unwrap(),
    ));
    assert_eq!(request(receiver, poison).await.0, StatusCode::FORBIDDEN);
    let unreachable = PgPoolOptions::new()
        .acquire_timeout(Duration::from_millis(100))
        .connect_lazy("postgres://postgres@127.0.0.1:1/unreachable")
        .unwrap();
    let cached_reader = app(db.clone(), Some(ImageStore::new(unreachable, false, None)));
    assert_eq!(
        request(
            cached_reader.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    let unpublished = uuid::Uuid::new_v4().to_string();
    isureal::image_api::remember_stored_image(
        unpublished.clone(),
        created,
        bytes::Bytes::copy_from_slice(original),
    );
    assert_eq!(
        request(
            cached_reader.clone(),
            Request::get(image_url(&unpublished, &key))
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    // The immutable cache token must stop matching if metadata switches back
    // to inline bytes, even though the externally visible image ID is unchanged.
    sqlx::query("UPDATE image_objects SET asset_key=NULL,data=$2 WHERE id=$1")
        .bind(&external_id)
        .bind(second)
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            cached_reader.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, second.to_vec())
    );
    sqlx::query("UPDATE image_objects SET asset_key=id,data=''::bytea WHERE id=$1")
        .bind(&external_id)
        .execute(&db)
        .await
        .unwrap();
    isureal::image_api::remember_stored_image(
        external_id.clone(),
        created,
        bytes::Bytes::copy_from_slice(original),
    );
    drop(cached_reader);

    assert_eq!(
        request(
            routed.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    // Metadata references replicate; independent image bytes do not. A regional
    // reader uses the reference and the same asset primary after replay.
    if let Ok(replica_dsn) = std::env::var("ISUREAL_FENCE_REPLICA_DSN") {
        let meta_options = PgConnectOptions::from_str(&dsn).unwrap();
        let replica = PgPoolOptions::new()
            .max_connections(2)
            .connect_with(
                PgConnectOptions::from_str(&replica_dsn)
                    .unwrap()
                    .database(meta_options.get_database().unwrap())
                    .options([("search_path", schema.as_str())]),
            )
            .await
            .unwrap();
        let lsn: String = sqlx::query_scalar("SELECT pg_current_wal_insert_lsn()::text")
            .fetch_one(&db)
            .await
            .unwrap();
        tokio::time::timeout(Duration::from_secs(3), async {
            while !sqlx::query_scalar::<_, bool>("SELECT pg_last_wal_replay_lsn() >= $1::pg_lsn")
                .bind(&lsn)
                .fetch_one(&replica)
                .await
                .unwrap()
            {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
        let asset_table: Option<String> =
            sqlx::query_scalar("SELECT to_regclass('astra_image_data')::text")
                .fetch_one(&replica)
                .await
                .unwrap();
        assert!(asset_table.is_none());
        assert_eq!(
            request(
                app(replica.clone(), Some(store.clone())),
                Request::get(&external_url).body(Body::empty()).unwrap()
            )
            .await,
            (StatusCode::OK, original.to_vec())
        );
        replica.close().await;
    }
    // A duplicate is rejected after storage; its orphan is never publicly visible.
    assert_eq!(
        request(routed.clone(), upload(original)).await.0,
        StatusCode::CONFLICT
    );
    assert_eq!(count(&db, "posts").await, 2);
    assert_eq!(count(&assets, "astra_image_data").await, 1);
    assert_eq!(
        request(routed.clone(), upload(second)).await.0,
        StatusCode::CONFLICT
    );
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    let orphan: String = sqlx::query_scalar("SELECT id FROM astra_image_data WHERE id<>$1")
        .bind(&external_id)
        .fetch_one(&assets)
        .await
        .unwrap();
    assert_eq!(
        request(
            routed.clone(),
            Request::get(image_url(&orphan, &key))
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        request(routed.clone(), upload(b"invalid JPEG")).await.0,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(count(&assets, "astra_image_data").await, 2);

    // Force failure after the function inserted its post. Both metadata rows roll back.
    next_window(&db, 1).await;
    sqlx::raw_sql("CREATE FUNCTION fail_asset_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced publication failure'; END $$; CREATE TRIGGER fail_asset_publish BEFORE UPDATE OF asset_key ON image_objects FOR EACH ROW EXECUTE FUNCTION fail_asset_publish()")
        .execute(&db).await.unwrap();
    assert_eq!(
        request(routed.clone(), upload(second)).await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(count(&db, "posts").await, 2);
    assert_eq!(count(&db, "image_objects").await, 2);
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    sqlx::raw_sql(
        "DROP TRIGGER fail_asset_publish ON image_objects; DROP FUNCTION fail_asset_publish()",
    )
    .execute(&db)
    .await
    .unwrap();

    // Switching off new external writes retains readers for already published bytes.
    let mut local_store = store.clone();
    local_store.writes_enabled = false;
    let local = app(db.clone(), Some(local_store));
    let (status, response) = request(local.clone(), upload(second)).await;
    assert_eq!(status, StatusCode::CREATED);
    let response: Value = serde_json::from_slice(&response).unwrap();
    let local_url = response["imageUrl"].as_str().unwrap();
    assert_eq!(
        request(
            local.clone(),
            Request::get(local_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, second.to_vec())
    );
    assert_eq!(
        request(
            local.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    let (status, archive) = request(
        local.clone(),
        Request::get("/api/me/export").body(Body::empty()).unwrap(),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let mut zip = zip::ZipArchive::new(Cursor::new(archive)).unwrap();
    for (index, expected) in [original, original, second].into_iter().enumerate() {
        let mut bytes = Vec::new();
        zip.by_name(&format!("images/{:06}.jpg", index + 1))
            .unwrap()
            .read_to_end(&mut bytes)
            .unwrap();
        assert_eq!(bytes, expected);
    }
    let mut manifest = String::new();
    zip.by_name("posts.json")
        .unwrap()
        .read_to_string(&mut manifest)
        .unwrap();
    let manifest: Value = serde_json::from_str(&manifest).unwrap();
    assert_eq!(manifest.as_array().unwrap().len(), 3);
    assert_eq!(manifest[0]["id"], "legacy");
    assert_eq!(manifest[1]["id"], post_id);

    // Cached external bytes cannot bypass fresh deletion, key or account checks.
    sqlx::query("UPDATE posts SET deleted_at=now() WHERE id=$1")
        .bind(&post_id)
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            local.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("UPDATE posts SET deleted_at=NULL WHERE id=$1")
        .bind(&post_id)
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("UPDATE users SET secret_key_hash=$1 WHERE id='owner'")
        .bind(hex::encode([9u8; 32]))
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            local.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        request(
            local.clone(),
            Request::get(image_url(&external_id, &[9u8; 32]))
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::OK
    );
    sqlx::query("UPDATE users SET secret_key_hash=$1,status='deleted' WHERE id='owner'")
        .bind(hex::encode(key))
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            local.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("UPDATE users SET status='active' WHERE id='owner'")
        .execute(&db)
        .await
        .unwrap();

    let missing_id = uuid::Uuid::new_v4().to_string();
    sqlx::query("INSERT INTO image_objects(id,owner_user_id,data,asset_key,media_type) VALUES ($1,'owner',''::bytea,$1,'image/jpeg')")
        .bind(&missing_id).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES ('missing','owner','window-1',$1,'public','none',now())")
        .bind(&missing_id).execute(&db).await.unwrap();
    assert_eq!(
        request(
            local.clone(),
            Request::get(image_url(&missing_id, &key))
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(
        request(
            local.clone(),
            Request::get("/api/me/export").body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    sqlx::query("DELETE FROM posts WHERE id='missing'")
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("DELETE FROM image_objects WHERE id=$1")
        .bind(&missing_id)
        .execute(&db)
        .await
        .unwrap();

    // Reset removes references while preserving initial images; it never races
    // in-flight publication by deleting independent immutable bytes.
    sqlx::raw_sql(include_str!("../../sql/reset.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("SET session_replication_role=origin")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(count(&db, "posts").await, 1);
    assert_eq!(count(&db, "image_objects").await, 1);
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    assert_eq!(
        request(
            local.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        request(
            local.clone(),
            Request::get(local_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        request(
            local.clone(),
            Request::get(image_url("legacy", &key))
                .body(Body::empty())
                .unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    // Reusing the immutable bytes after reset must create a new, separately
    // authorized public image. The old URL remains forbidden.
    next_window(&db, 0).await;
    let (status, response) = request(routed.clone(), upload(original)).await;
    assert_eq!(status, StatusCode::CREATED);
    let response: Value = serde_json::from_slice(&response).unwrap();
    let shared_url = response["imageUrl"].as_str().unwrap();
    let shared_key: String = sqlx::query_scalar(
        "SELECT i.asset_key FROM image_objects i JOIN posts p ON p.image_id=i.id WHERE p.id=$1",
    )
    .bind(response["id"].as_str().unwrap())
    .fetch_one(&db)
    .await
    .unwrap();
    assert_eq!(shared_key, external_id);
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    assert_eq!(
        request(
            routed.clone(),
            Request::get(shared_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    assert_eq!(
        request(
            routed.clone(),
            Request::get(&external_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    // A fresh process reads shared references from PostgreSQL after caches reset.
    let fresh_store = ImageStore::new(assets.clone(), false, None);
    assert_eq!(fresh_store.get(&shared_key).await.unwrap(), original);
    let fresh = app(db.clone(), Some(fresh_store));
    assert_eq!(
        request(fresh, Request::get(shared_url).body(Body::empty()).unwrap()).await,
        (StatusCode::OK, original.to_vec())
    );
    let other_id = uuid::Uuid::new_v4().to_string();
    let other_key = [0xcdu8; 32];
    sqlx::query("INSERT INTO users(id,username,secret_key_hash,display_name,region) VALUES ('other','other',$1,'Other','asia')")
        .bind(hex::encode(other_key)).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO image_objects(id,owner_user_id,data,asset_key,media_type) VALUES ($1,'other',''::bytea,$2,'image/jpeg')")
        .bind(&other_id).bind(&shared_key).execute(&db).await.unwrap();
    sqlx::query("INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) SELECT 'other','other',posting_window_id,$1,'public','none',now() FROM posts WHERE id=$2")
        .bind(&other_id).bind(response["id"].as_str().unwrap()).execute(&db).await.unwrap();
    assert_eq!(
        request(
            routed.clone(),
            Request::get(image_url(&other_id, &key))
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    sqlx::query("UPDATE posts SET deleted_at=now() WHERE id=$1")
        .bind(response["id"].as_str().unwrap())
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            routed.clone(),
            Request::get(shared_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        request(
            routed.clone(),
            Request::get(image_url(&other_id, &other_key))
                .body(Body::empty())
                .unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    let uncached_store = ImageStore::new(assets.clone(), true, None).with_cache_enabled(false);
    let uncached_id = uuid::Uuid::new_v4().to_string();
    let uncached_id2 = uuid::Uuid::new_v4().to_string();
    assert_eq!(
        uncached_store
            .put(&uncached_id, &bytes::Bytes::copy_from_slice(original))
            .await
            .unwrap(),
        uncached_id
    );
    assert_eq!(
        uncached_store
            .put(&uncached_id2, &bytes::Bytes::copy_from_slice(original))
            .await
            .unwrap(),
        uncached_id2
    );
    assert_eq!(uncached_store.get(&shared_key).await.unwrap(), original);
    assert_eq!(uncached_store.get(&uncached_id).await.unwrap(), original);
    drop(routed);
    drop(local);
    drop(store);
    db.close().await;
    assets.close().await;
    for db in [&admin, &asset_admin] {
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(db)
            .await
            .unwrap();
    }
    admin.close().await;
    asset_admin.close().await;
}
