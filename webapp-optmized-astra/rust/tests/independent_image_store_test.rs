use axum::{
    Extension, Router,
    body::Body,
    http::{Request, StatusCode},
    routing::{delete, get, post, put},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::Utc;
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env,
    export_api::export_posts_handler,
    image_api::get_image_handler,
    image_signature::image_signature,
    image_store::ImageStore,
    post_api::create_post_handler,
    profile_image_api::{delete_profile_image_handler, put_profile_image_handler},
    realmoji_api::{delete_real_moji_handler, replace_real_moji_handler},
    session::Session,
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
        ("reactionType", "like"),
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
        .route(
            "/api/me/profile-image",
            put(put_profile_image_handler).delete(delete_profile_image_handler),
        )
        .route(
            "/api/posts/{postId}/realmojis",
            post(replace_real_moji_handler),
        )
        .route(
            "/api/realmojis/{realMojiId}",
            delete(delete_real_moji_handler),
        )
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
    sqlx::raw_sql(include_str!("../../sql/013-atomic-post-reference.sql"))
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
    // Export validates current metadata before reusing bytes; a warm immutable
    // image does not need another connection to the external store.
    let (status, archive) = request(
        cached_reader.clone(),
        Request::get("/api/me/export").body(Body::empty()).unwrap(),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let mut zip = zip::ZipArchive::new(Cursor::new(archive)).unwrap();
    let mut exported = Vec::new();
    zip.by_name("images/000002.jpg")
        .unwrap()
        .read_to_end(&mut exported)
        .unwrap();
    assert_eq!(exported, original);
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
    let (status, archive) = request(
        cached_reader.clone(),
        Request::get("/api/me/export").body(Body::empty()).unwrap(),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let mut zip = zip::ZipArchive::new(Cursor::new(archive)).unwrap();
    let mut exported = Vec::new();
    zip.by_name("images/000002.jpg")
        .unwrap()
        .read_to_end(&mut exported)
        .unwrap();
    assert_eq!(exported, second);
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
    sqlx::raw_sql("CREATE FUNCTION fail_asset_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced publication failure'; END $$; CREATE TRIGGER fail_asset_publish AFTER INSERT ON posts FOR EACH ROW EXECUTE FUNCTION fail_asset_publish()")
        .execute(&db).await.unwrap();
    assert_eq!(
        request(routed.clone(), upload(second)).await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(count(&db, "posts").await, 2);
    assert_eq!(count(&db, "image_objects").await, 2);
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    sqlx::raw_sql("DROP TRIGGER fail_asset_publish ON posts; DROP FUNCTION fail_asset_publish()")
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
    sqlx::raw_sql(&format!(
        "{}\nSET session_replication_role=origin;",
        include_str!("../../sql/reset.sql")
    ))
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
    // Profile replacement and realmoji replacement keep independent public
    // lifetimes even when every image refers to the same immutable bytes.
    let mut previous_profile = None;
    for _ in 0..2 {
        assert_eq!(
            request(
                routed.clone(),
                Request::put("/api/me/profile-image")
                    .header("content-type", "image/jpeg")
                    .body(Body::from(original.to_vec()))
                    .unwrap()
            )
            .await
            .0,
            StatusCode::NO_CONTENT
        );
        let profile_id: String =
            sqlx::query_scalar("SELECT profile_image_id FROM users WHERE id='owner'")
                .fetch_one(&db)
                .await
                .unwrap();
        let reference: (String, i32) =
            sqlx::query_as("SELECT asset_key,octet_length(data) FROM image_objects WHERE id=$1")
                .bind(&profile_id)
                .fetch_one(&db)
                .await
                .unwrap();
        assert_eq!(reference, (shared_key.clone(), 0));
        assert_eq!(
            request(
                routed.clone(),
                Request::get(image_url(&profile_id, &key))
                    .body(Body::empty())
                    .unwrap()
            )
            .await,
            (StatusCode::OK, original.to_vec())
        );
        if let Some(previous) = previous_profile {
            assert_eq!(
                request(
                    routed.clone(),
                    Request::get(previous).body(Body::empty()).unwrap()
                )
                .await
                .0,
                StatusCode::FORBIDDEN
            );
        }
        previous_profile = Some(image_url(&profile_id, &key));
    }
    sqlx::query("UPDATE posts SET deleted_at=NULL WHERE id=$1")
        .bind(response["id"].as_str().unwrap())
        .execute(&db)
        .await
        .unwrap();
    let mut previous_reaction = None;
    let mut reaction_id = String::new();
    for _ in 0..2 {
        let mut upload = upload(original);
        *upload.uri_mut() = "/api/posts/other/realmojis".parse().unwrap();
        let (status, response) = request(routed.clone(), upload).await;
        assert_eq!(
            status,
            StatusCode::CREATED,
            "{}",
            String::from_utf8_lossy(&response)
        );
        let reaction: Value = serde_json::from_slice(&response).unwrap();
        reaction_id = reaction["id"].as_str().unwrap().to_owned();
        let url = reaction["imageUrl"].as_str().unwrap().to_owned();
        let reference: (String, i32) = sqlx::query_as("SELECT i.asset_key,octet_length(i.data) FROM image_objects i JOIN realmojis r ON r.image_id=i.id WHERE r.id=$1")
            .bind(&reaction_id).fetch_one(&db).await.unwrap();
        assert_eq!(reference, (shared_key.clone(), 0));
        assert_eq!(
            request(
                routed.clone(),
                Request::get(&url).body(Body::empty()).unwrap()
            )
            .await,
            (StatusCode::OK, original.to_vec())
        );
        if let Some(previous) = previous_reaction {
            assert_eq!(
                request(
                    routed.clone(),
                    Request::get(previous).body(Body::empty()).unwrap()
                )
                .await
                .0,
                StatusCode::FORBIDDEN
            );
        }
        previous_reaction = Some(url);
    }
    assert_eq!(
        request(
            routed.clone(),
            Request::delete("/api/me/profile-image")
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        request(
            routed.clone(),
            Request::get(previous_profile.unwrap())
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        request(
            routed.clone(),
            Request::get(previous_reaction.as_ref().unwrap())
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        request(
            routed.clone(),
            Request::delete(format!("/api/realmojis/{reaction_id}"))
                .body(Body::empty())
                .unwrap()
        )
        .await
        .0,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        request(
            routed.clone(),
            Request::get(previous_reaction.unwrap())
                .body(Body::empty())
                .unwrap()
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
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(count(&assets, "astra_image_data").await, 2);
    // The rollout switch also preserves inline profile writes and rejects
    // invalid oversized images before publication.
    assert_eq!(
        request(
            local.clone(),
            Request::put("/api/me/profile-image")
                .header("content-type", "image/jpeg")
                .body(Body::from(original.to_vec()))
                .unwrap()
        )
        .await
        .0,
        StatusCode::NO_CONTENT
    );
    let inline: (Option<String>, i32) = sqlx::query_as("SELECT i.asset_key,octet_length(i.data) FROM image_objects i JOIN users u ON u.profile_image_id=i.id WHERE u.id='owner'")
        .fetch_one(&db).await.unwrap();
    assert_eq!(inline, (None, original.len() as i32));
    assert_eq!(
        request(
            routed.clone(),
            Request::put("/api/me/profile-image")
                .header("content-type", "image/jpeg")
                .body(Body::from(second.to_vec()))
                .unwrap()
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
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
    // The regional ingress keeps already-uploaded bytes only after the actual
    // writer has committed. Its first image read must work without an asset
    // connection, while the fresh metadata/owner checks still apply.
    next_window(&db, 2).await;
    let upstream = app(
        db.clone(),
        Some(ImageStore::new(assets.clone(), true, None).with_cache_enabled(false)),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let proxy = isureal::upload_proxy::UploadProxy::new(listener.local_addr().unwrap());
    let upstream_task = tokio::spawn(async move { axum::serve(listener, upstream).await.unwrap() });
    let ingress = Router::new()
        .fallback(|| async { StatusCode::IM_A_TEAPOT })
        .layer(axum::middleware::from_fn_with_state(
            proxy,
            isureal::upload_proxy::upload_proxy_middleware,
        ));
    let (status, response) = request(ingress.clone(), upload(original)).await;
    assert_eq!(
        status,
        StatusCode::CREATED,
        "{}",
        String::from_utf8_lossy(&response)
    );
    let response: Value = serde_json::from_slice(&response).unwrap();
    let proxied_url = response["imageUrl"].as_str().unwrap();
    let offline = PgPoolOptions::new()
        .acquire_timeout(Duration::from_millis(100))
        .connect_lazy("postgres://postgres@127.0.0.1:1/unreachable")
        .unwrap();
    let regional_reader = app(
        db.clone(),
        Some(ImageStore::new(offline, false, None).with_cache_enabled(false)),
    );
    assert_eq!(
        request(
            regional_reader.clone(),
            Request::get(proxied_url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    sqlx::query("UPDATE posts SET posting_window_id='window-2' WHERE id='other'")
        .execute(&db)
        .await
        .unwrap();
    let mut reaction_upload = upload(original);
    *reaction_upload.uri_mut() = "/api/posts/other/realmojis".parse().unwrap();
    let (status, reaction) = request(ingress, reaction_upload).await;
    assert_eq!(
        status,
        StatusCode::CREATED,
        "{}",
        String::from_utf8_lossy(&reaction)
    );
    let reaction: Value = serde_json::from_slice(&reaction).unwrap();
    let reaction_url = reaction["imageUrl"].as_str().unwrap();
    // Force a different metadata timestamp to exercise the immutable-key
    // fallback independently of clock precision in the upload response.
    sqlx::query("UPDATE image_objects SET created_at=created_at-interval '1 second' WHERE id=(SELECT image_id FROM realmojis WHERE id=$1)")
        .bind(reaction["id"].as_str().unwrap()).execute(&db).await.unwrap();
    for _ in 0..2 {
        assert_eq!(
            request(
                regional_reader.clone(),
                Request::get(reaction_url).body(Body::empty()).unwrap()
            )
            .await,
            (StatusCode::OK, original.to_vec())
        );
    }
    sqlx::query("UPDATE realmojis SET deleted_at=now() WHERE id=$1")
        .bind(reaction["id"].as_str().unwrap())
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            regional_reader.clone(),
            Request::get(reaction_url).body(Body::empty()).unwrap()
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
            regional_reader,
            Request::get(proxied_url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    upstream_task.abort();
    // An independent regional cluster owns the bytes; metadata carries only a
    // signed reference, and a fresh reader can retrieve them after cache loss.
    if let Ok(regional_dsn) = std::env::var("ISUREAL_REGIONAL_ASSET_TEST_DSN") {
        use isureal::{
            regional_upload::RegionalUpload,
            stored_upload::{UploadAuthenticator, authenticate},
        };
        use sha2::{Digest, Sha256};
        let region_admin = PgPool::connect(&regional_dsn).await.unwrap();
        let region_id: i64 =
            sqlx::query_scalar("SELECT system_identifier::bigint FROM pg_control_system()")
                .fetch_one(&region_admin)
                .await
                .unwrap();
        assert_ne!(region_id, asset_id);
        assert_ne!(region_id, primary_id);
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&region_admin)
            .await
            .unwrap();
        let region_pool = pool(&regional_dsn, &schema).await;
        sqlx::raw_sql("CREATE TABLE astra_image_data(id varchar(36) PRIMARY KEY,data bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp())").execute(&region_pool).await.unwrap();
        let regional_store = ImageStore::new(assets.clone(), true, None)
            .with_cache_enabled(false)
            .with_regional_pools([("europe".into(), region_pool.clone())].into());
        sqlx::query("UPDATE users SET region='europe' WHERE id='owner'")
            .execute(&db)
            .await
            .unwrap();
        next_window(&db, 3).await;
        sqlx::query("UPDATE posting_windows SET region='europe' WHERE id='window-3'")
            .execute(&db)
            .await
            .unwrap();
        sqlx::query("INSERT INTO login_sessions(id,user_id,token_hash) VALUES ('regional-session','owner',$1)").bind(hex::encode(Sha256::digest(b"regional-token"))).execute(&db).await.unwrap();
        let handoff_key = [42; 32];
        let upstream = app(db.clone(), Some(regional_store.clone())).layer(
            axum::middleware::from_fn_with_state(
                UploadAuthenticator::new(
                    handoff_key,
                    [("127.0.0.1".parse().unwrap(), "europe".into())].into(),
                ),
                authenticate,
            ),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let offload = RegionalUpload::new(
            db.clone(),
            regional_store.clone(),
            "europe".into(),
            handoff_key,
        );
        let proxy = isureal::upload_proxy::UploadProxy::new(listener.local_addr().unwrap())
            .with_regional_upload(offload.clone());
        let task = tokio::spawn(async move {
            axum::serve(
                listener,
                upstream.into_make_service_with_connect_info::<std::net::SocketAddr>(),
            )
            .await
            .unwrap()
        });
        let ingress = Router::new()
            .fallback(|| async { StatusCode::IM_A_TEAPOT })
            .layer(axum::middleware::from_fn_with_state(
                proxy,
                isureal::upload_proxy::upload_proxy_middleware,
            ));
        let make_upload = || {
            let mut r = upload(original);
            r.headers_mut()
                .insert("authorization", "Bearer regional-token".parse().unwrap());
            r.headers_mut()
                .insert("x-client-ip", "192.0.2.5".parse().unwrap());
            r
        };
        let invalid = upload(original);
        let (parts, body) = invalid.into_parts();
        let body = axum::body::to_bytes(body, 32 * 1024 * 1024).await.unwrap();
        assert!(
            offload
                .prepare(
                    &parts.headers,
                    "192.0.2.5",
                    "multipart/form-data; boundary=boundary",
                    body
                )
                .await
                .unwrap()
                .is_none()
        );
        assert_eq!(count(&region_pool, "astra_image_data").await, 0);
        let before = count(&assets, "astra_image_data").await;
        let (status, response) = request(ingress.clone(), make_upload()).await;
        assert_eq!(
            status,
            StatusCode::CREATED,
            "{}",
            String::from_utf8_lossy(&response)
        );
        let response: Value = serde_json::from_slice(&response).unwrap();
        let post_id = response["id"].as_str().unwrap();
        let image_url = response["imageUrl"].as_str().unwrap();
        let (reference,inline):(String,i32)=sqlx::query_as("SELECT i.asset_key,octet_length(i.data) FROM image_objects i JOIN posts p ON p.image_id=i.id WHERE p.id=$1").bind(post_id).fetch_one(&db).await.unwrap();
        assert!(reference.starts_with("europe:"));
        assert_eq!(inline, 0);
        assert_eq!(count(&assets, "astra_image_data").await, before);
        assert_eq!(count(&region_pool, "astra_image_data").await, 1);
        let fresh_store = ImageStore::new(assets.clone(), false, None)
            .with_cache_enabled(false)
            .with_regional_pools([("europe".into(), region_pool.clone())].into());
        assert_eq!(fresh_store.get(&reference).await.unwrap(), original);
        let fresh = app(db.clone(), Some(fresh_store));
        assert_eq!(
            request(
                fresh.clone(),
                Request::get(image_url).body(Body::empty()).unwrap()
            )
            .await,
            (StatusCode::OK, original.to_vec())
        );
        let (status, archive) = request(
            fresh.clone(),
            Request::get("/api/me/export").body(Body::empty()).unwrap(),
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        let mut zip = zip::ZipArchive::new(Cursor::new(archive)).unwrap();
        let mut manifest = String::new();
        zip.by_name("posts.json")
            .unwrap()
            .read_to_string(&mut manifest)
            .unwrap();
        let manifest: Value = serde_json::from_str(&manifest).unwrap();
        let index = manifest
            .as_array()
            .unwrap()
            .iter()
            .position(|p| p["id"] == post_id)
            .unwrap();
        let mut bytes = Vec::new();
        zip.by_name(&format!("images/{:06}.jpg", index + 1))
            .unwrap()
            .read_to_end(&mut bytes)
            .unwrap();
        assert_eq!(bytes, original);
        // Duplicate publication can leave immutable private bytes, never another post.
        let post_count = count(&db, "posts").await;
        assert_eq!(
            request(ingress.clone(), make_upload()).await.0,
            StatusCode::CONFLICT
        );
        assert_eq!(count(&db, "posts").await, post_count);
        // A successful regional preflight never authorizes publication after
        // logout. The writer must still run its fresh session middleware.
        let (parts, body) = make_upload().into_parts();
        let body = axum::body::to_bytes(body, 32 * 1024 * 1024).await.unwrap();
        let prepared = offload
            .prepare(
                &parts.headers,
                "192.0.2.5",
                "multipart/form-data; boundary=boundary",
                body.clone(),
            )
            .await
            .unwrap()
            .unwrap();
        assert!(prepared.body.len() < body.len());
        assert!(
            !prepared
                .body
                .windows(original.len())
                .any(|bytes| bytes == original)
        );
        let h = Arc::new(HandlerContext {
            db: db.clone(),
            config: config_from_env(),
            ip_bans: Default::default(),
            notifications: Default::default(),
            read_fence: None,
            image_store: Some(regional_store.clone()),
        });
        let authenticated_writer = Router::new()
            .route("/api/posts", post(create_post_handler))
            .route_layer(axum::middleware::from_fn_with_state(
                (h.clone(), true),
                isureal::session::session_middleware,
            ))
            .with_state(h)
            .layer(axum::middleware::from_fn_with_state(
                UploadAuthenticator::new(
                    handoff_key,
                    [("127.0.0.1".parse().unwrap(), "europe".into())].into(),
                ),
                authenticate,
            ));
        let mut forwarded_headers = parts.headers;
        forwarded_headers.insert("content-type", prepared.content_type.parse().unwrap());
        let proof = isureal::stored_upload::sign(
            &handoff_key,
            prepared.reference.clone(),
            "/api/posts",
            &forwarded_headers,
            &prepared.body,
        );
        let mut forwarded = Request::post("/api/posts")
            .body(Body::from(prepared.body))
            .unwrap();
        *forwarded.headers_mut() = forwarded_headers;
        forwarded
            .headers_mut()
            .insert(isureal::stored_upload::HEADER, proof.parse().unwrap());
        forwarded
            .extensions_mut()
            .insert(axum::extract::ConnectInfo(
                "127.0.0.1:12345".parse::<std::net::SocketAddr>().unwrap(),
            ));
        sqlx::query("UPDATE login_sessions SET active=false WHERE id='regional-session'")
            .execute(&db)
            .await
            .unwrap();
        let post_count = count(&db, "posts").await;
        assert_eq!(
            request(authenticated_writer, forwarded).await.0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(count(&db, "posts").await, post_count);
        sqlx::query("UPDATE login_sessions SET active=true WHERE id='regional-session'")
            .execute(&db)
            .await
            .unwrap();
        sqlx::query(
            "INSERT INTO ip_bans(ip,banned_until) VALUES ('192.0.2.5',now()+interval '1 hour')",
        )
        .execute(&db)
        .await
        .unwrap();
        let (parts, body) = make_upload().into_parts();
        let body = axum::body::to_bytes(body, 32 * 1024 * 1024).await.unwrap();
        let count_before = count(&region_pool, "astra_image_data").await;
        assert!(
            offload
                .prepare(
                    &parts.headers,
                    "192.0.2.5",
                    "multipart/form-data; boundary=boundary",
                    body
                )
                .await
                .unwrap()
                .is_none()
        );
        assert_eq!(count(&region_pool, "astra_image_data").await, count_before);
        sqlx::query("UPDATE posts SET deleted_at=now() WHERE id=$1")
            .bind(post_id)
            .execute(&db)
            .await
            .unwrap();
        assert_eq!(
            request(fresh, Request::get(image_url).body(Body::empty()).unwrap())
                .await
                .0,
            StatusCode::FORBIDDEN
        );
        task.abort();
        region_pool.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&region_admin)
            .await
            .unwrap();
        region_admin.close().await;
    }
    // Files use the same metadata authority and publication order as the SQL
    // stores, while image bytes no longer enter an asset table or its WAL.
    sqlx::raw_sql("UPDATE users SET region='asia',status='active',secret_key_hash=repeat('ab',32) WHERE id='owner'; UPDATE posts SET deleted_at=now() WHERE author_id='owner';").execute(&db).await.unwrap();
    next_window(&db, 1000).await;
    let file_root =
        std::env::temp_dir().join(format!("astra-published-files-{}", uuid::Uuid::new_v4()));
    let files = isureal::file_store::FileStore::new(
        file_root.clone(),
        "asia".into(),
        Default::default(),
        true,
    );
    files.prepare().await.unwrap();
    let file_store = ImageStore::new(assets.clone(), true, None)
        .with_cache_enabled(false)
        .with_file_store(Some(files.clone()));
    let file_app = app(db.clone(), Some(file_store));
    let asset_count = count(&assets, "astra_image_data").await;
    let (status, response) = request(file_app.clone(), upload(original)).await;
    assert_eq!(
        status,
        StatusCode::CREATED,
        "{}",
        String::from_utf8_lossy(&response)
    );
    let response: Value = serde_json::from_slice(&response).unwrap();
    let file_post = response["id"].as_str().unwrap();
    let file_image: String = sqlx::query_scalar("SELECT image_id FROM posts WHERE id=$1")
        .bind(file_post)
        .fetch_one(&db)
        .await
        .unwrap();
    let (length, key): (i32, String) =
        sqlx::query_as("SELECT octet_length(data),asset_key FROM image_objects WHERE id=$1")
            .bind(&file_image)
            .fetch_one(&db)
            .await
            .unwrap();
    assert_eq!(length, 0);
    assert!(key.starts_with("fs:asia:"));
    assert_eq!(count(&assets, "astra_image_data").await, asset_count);
    assert_eq!(files.get(&key).await.unwrap(), original);
    let url = image_url(&file_image, &[0xab; 32]);
    assert_eq!(
        request(
            file_app.clone(),
            Request::get(&url).body(Body::empty()).unwrap()
        )
        .await,
        (StatusCode::OK, original.to_vec())
    );
    let (status, archive) = request(
        file_app.clone(),
        Request::get("/api/me/export").body(Body::empty()).unwrap(),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let mut zip = zip::ZipArchive::new(Cursor::new(archive)).unwrap();
    let mut exported = Vec::new();
    zip.by_name("images/000001.jpg")
        .unwrap()
        .read_to_end(&mut exported)
        .unwrap();
    assert_eq!(exported, original);
    next_window(&db, 1001).await;
    sqlx::raw_sql("CREATE FUNCTION reject_file_post() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced file publication failure'; END $$; CREATE TRIGGER reject_file_post BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION reject_file_post();").execute(&db).await.unwrap();
    let before = count(&db, "image_objects").await;
    assert_eq!(
        request(file_app.clone(), upload(original)).await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(count(&db, "image_objects").await, before);
    sqlx::query("DROP TRIGGER reject_file_post ON posts")
        .execute(&db)
        .await
        .unwrap();
    let restored = isureal::file_store::FileStore::new(
        file_root.clone(),
        "asia".into(),
        Default::default(),
        false,
    );
    restored.prepare().await.unwrap();
    assert_eq!(restored.get(&key).await.unwrap(), original);
    sqlx::query("UPDATE posts SET deleted_at=now() WHERE id=$1")
        .bind(file_post)
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        request(
            file_app.clone(),
            Request::get(&url).body(Body::empty()).unwrap()
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    sqlx::raw_sql(&format!(
        "{}\nSET session_replication_role=origin;",
        include_str!("../../sql/reset.sql")
    ))
    .execute(&db)
    .await
    .unwrap();
    assert_eq!(
        request(file_app, Request::get(&url).body(Body::empty()).unwrap())
            .await
            .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(restored.get(&key).await.unwrap(), original);
    std::fs::remove_dir_all(file_root).unwrap();
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
