use axum::{
    Extension, Router,
    body::Body,
    http::{Request, StatusCode},
    routing::get,
};
use bytes::Bytes;
use chrono::{DateTime, NaiveDate, Utc};
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env, export_api::export_posts_handler, file_store::FileStore,
    image_store::ImageStore, session::Session,
};
use isureal::{initial_history::InitialHistory, memory_api::list_memories};
use sqlx::{
    PgConnection, PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{
    io::{Cursor, Read},
    str::FromStr,
    sync::Arc,
};
use tower::ServiceExt;

async fn export_contents(app: &Router) -> (serde_json::Value, Vec<Vec<u8>>) {
    let response = app
        .clone()
        .oneshot(Request::get("/export").body(Body::empty()).unwrap())
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    let mut zip = zip::ZipArchive::new(Cursor::new(bytes)).unwrap();
    let mut manifest = Vec::new();
    zip.by_name("posts.json")
        .unwrap()
        .read_to_end(&mut manifest)
        .unwrap();
    let value: serde_json::Value = serde_json::from_slice(&manifest).unwrap();
    let mut images = Vec::new();
    for post in value.as_array().unwrap() {
        let mut image = Vec::new();
        zip.by_name(post["imagePath"].as_str().unwrap())
            .unwrap()
            .read_to_end(&mut image)
            .unwrap();
        images.push(image);
    }
    (value, images)
}
fn export_app(db: PgPool, store: ImageStore, history: Option<Arc<InitialHistory>>) -> Router {
    let context = Arc::new(HandlerContext {
        db,
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: false,
        read_fence: None,
        image_store: Some(store),
    });
    let app = Router::new()
        .route("/export", get(export_posts_handler))
        .with_state(context)
        .layer(Extension(Session {
            user_id: OWNER.into(),
            token_hash: String::new(),
            region: "asia".into(),
            created_at: Utc::now(),
            token: String::new(),
        }));
    if let Some(history) = history {
        app.layer(Extension(history))
    } else {
        app
    }
}

async fn compare(
    cache: &InitialHistory,
    db: &mut PgConnection,
    before: Option<DateTime<Utc>>,
    id: &str,
) {
    let start = NaiveDate::from_ymd_opt(2026, 9, 1).unwrap();
    let end = NaiveDate::from_ymd_opt(2026, 10, 1).unwrap();
    let cached = cache
        .memories(db, OWNER, start, end, before, id)
        .await
        .unwrap()
        .expect("clean initial month");
    let sql = list_memories(db, OWNER, start, end, before, id, &mut None)
        .await
        .unwrap();
    assert_eq!(
        serde_json::to_value(cached).unwrap(),
        serde_json::to_value(sql).unwrap()
    );
}
const OWNER: &str = "00000000-0000-0000-0000-000000000001";
const OTHER: &str = "00000000-0000-0000-0000-000000000002";
#[tokio::test]
async fn initial_history_matches_live_sql_and_falls_back_on_source_changes() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_initial_history_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(2)
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
 CREATE TABLE users(id text PRIMARY KEY,username text,secret_key_hash text DEFAULT repeat('ab',32),display_name text DEFAULT 'Name',bio text DEFAULT '',profile_image_id text,region text DEFAULT 'asia',account_type text DEFAULT 'regular',status text DEFAULT 'active');
 CREATE TABLE posting_windows(id text PRIMARY KEY,local_date date,on_time_deadline timestamptz,ended_at timestamptz);
 CREATE TABLE image_objects(id text,created_at timestamptz,data bytea DEFAULT '',asset_key text,PRIMARY KEY(id,created_at));
 CREATE TABLE posts(id text PRIMARY KEY,author_id text,posting_window_id text,image_id text,caption text DEFAULT '',visibility text DEFAULT 'public',location_kind text DEFAULT 'none',latitude numeric,longitude numeric,locality text,posted_at timestamptz,created_at timestamptz DEFAULT '2026-09-01',deleted_at timestamptz);
 CREATE TABLE comments(id text PRIMARY KEY,post_id text,author_id text,body text,created_at timestamptz,deleted_at timestamptz);
 CREATE TABLE realmojis(id text PRIMARY KEY,post_id text,reactor_id text,image_id text,reaction_type text,created_at timestamptz,replaced_at timestamptz,deleted_at timestamptz);
 INSERT INTO users(id,username) VALUES('00000000-0000-0000-0000-000000000001','owner'),('00000000-0000-0000-0000-000000000002','other');
 CREATE TABLE initial_users AS SELECT * FROM users;
 CREATE TABLE initial_data_boundary AS SELECT '2026-10-01'::timestamptz AS created_at;
 INSERT INTO posting_windows VALUES('00000000-0000-0000-0000-000000000010','2026-09-01','2026-09-01 10:02Z','2026-09-02');
 INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at)
 SELECT md5('post'||i)::uuid::text,'00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000010',md5('image'||i)::uuid::text,'2026-09-01 10:00Z'::timestamptz + ((i/2)||' seconds')::interval FROM generate_series(1,55)i;
 INSERT INTO comments SELECT md5('comment'||p.id||i)::uuid::text,p.id,'00000000-0000-0000-0000-000000000002','body'||i,'2026-09-01 11:00Z'::timestamptz+((i/2)||' seconds')::interval,NULL FROM posts p CROSS JOIN generate_series(1,52)i;
 INSERT INTO realmojis SELECT md5('reaction'||p.id||i)::uuid::text,p.id,'00000000-0000-0000-0000-000000000002',md5('reaction-image'||p.id||i)::uuid::text,'smile','2026-09-01 11:00Z'::timestamptz+((i/2)||' seconds')::interval,NULL,NULL FROM posts p CROSS JOIN generate_series(1,52)i;
 UPDATE comments SET deleted_at=now() WHERE id=(SELECT min(id) FROM comments);
 UPDATE realmojis SET replaced_at=now() WHERE id=(SELECT min(id) FROM realmojis);
 "#).execute(&db).await.unwrap();
    let root = std::env::temp_dir().join(format!("astra-initial-export-{}", uuid::Uuid::new_v4()));
    let files = FileStore::new(root.clone(), "asia".into(), Default::default(), true);
    files.prepare().await.unwrap();
    let ids: Vec<String> = sqlx::query_scalar("SELECT image_id FROM posts")
        .fetch_all(&db)
        .await
        .unwrap();
    for id in ids {
        let key = files
            .put(&id, &Bytes::from(format!("image:{id}")))
            .await
            .unwrap();
        sqlx::query("INSERT INTO image_objects VALUES($1,'2026-09-01 10:00Z','',$2)")
            .bind(id)
            .bind(key)
            .execute(&db)
            .await
            .unwrap();
    }
    let migration = include_str!("../../sql/020-initial-history.sql");
    sqlx::raw_sql(migration).execute(&db).await.unwrap();
    sqlx::raw_sql(include_str!("../../sql/021-initial-export.sql"))
        .execute(&db)
        .await
        .unwrap();
    let cache = Arc::new(
        InitialHistory::load_with_export(&db, "asia", true)
            .await
            .unwrap(),
    );
    let store = ImageStore::new(db.clone(), true, None)
        .with_cache_enabled(false)
        .with_file_store(Some(files.clone()));
    let cached_app = export_app(db.clone(), store.clone(), Some(cache.clone()));
    let sql_app = export_app(db.clone(), store, None);
    let (manifest, images) = export_contents(&cached_app).await;
    assert_eq!(manifest.as_array().unwrap().len(), 55);
    assert_eq!(images.len(), 55);
    assert_eq!((manifest, images), export_contents(&sql_app).await);
    let mut conn = db.acquire().await.unwrap();
    compare(&cache, &mut conn, None, "").await;
    let (before, id): (DateTime<Utc>, String) = sqlx::query_as(
        "SELECT posted_at,id FROM posts ORDER BY posted_at DESC,id DESC OFFSET 49 LIMIT 1",
    )
    .fetch_one(&mut *conn)
    .await
    .unwrap();
    compare(&cache, &mut conn, Some(before), &id).await;
    let start = NaiveDate::from_ymd_opt(2026, 9, 1).unwrap();
    let end = NaiveDate::from_ymd_opt(2026, 10, 1).unwrap();
    assert!(
        cache
            .memories(&mut conn, OWNER, start, end, None, "bad")
            .await
            .is_err()
    );
    assert!(
        cache
            .memories(&mut conn, OWNER, start, end, Some(before), "non-uuid")
            .await
            .unwrap()
            .is_none()
    );
    assert!(
        cache
            .memories(&mut conn, OTHER, start, end, None, "")
            .await
            .unwrap()
            .unwrap()
            .items
            .is_empty()
    );
    // Live user fields and filtering must never be embedded in the snapshot.
    for change in [
        "UPDATE users SET display_name='Changed',bio='New',account_type='certified',profile_image_id='00000000-0000-0000-0000-000000000003',secret_key_hash=repeat('cd',32)",
        "UPDATE users SET status='deleted' WHERE username='other'",
        "UPDATE users SET status='deleted' WHERE username='owner'",
        "UPDATE posting_windows SET ended_at=NULL",
    ] {
        sqlx::query("BEGIN").execute(&mut *conn).await.unwrap();
        sqlx::raw_sql(change).execute(&mut *conn).await.unwrap();
        compare(&cache, &mut conn, None, "").await;
        sqlx::query("ROLLBACK").execute(&mut *conn).await.unwrap();
    }
    // Dirty tracking is transactional, including a changed 51st sentinel, edits,
    // new/removed interactions, new posts, window changes and explicit truncation.
    for change in [
        "UPDATE posts SET caption='edited' WHERE id=(SELECT id FROM posts ORDER BY posted_at DESC,id DESC OFFSET 50 LIMIT 1)",
        "UPDATE posts SET deleted_at=now() WHERE id=(SELECT min(id) FROM posts)",
        "UPDATE posts SET posted_at=posted_at+interval '1 second' WHERE id=(SELECT min(id) FROM posts)",
        "UPDATE comments SET body='edited' WHERE id=(SELECT min(id) FROM comments)",
        "DELETE FROM comments WHERE id=(SELECT max(id) FROM comments)",
        "UPDATE realmojis SET replaced_at=now() WHERE id=(SELECT max(id) FROM realmojis)",
        "INSERT INTO comments VALUES(gen_random_uuid()::text,(SELECT min(id) FROM posts),'00000000-0000-0000-0000-000000000002','new',now(),NULL)",
        "INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at,created_at) VALUES(gen_random_uuid()::text,'00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000010',gen_random_uuid()::text,now(),now())",
        "UPDATE posting_windows SET on_time_deadline=on_time_deadline+interval '1 second'",
        "UPDATE posting_windows SET local_date='2026-09-02'",
        "TRUNCATE comments",
    ] {
        sqlx::query("BEGIN").execute(&mut *conn).await.unwrap();
        sqlx::raw_sql(change).execute(&mut *conn).await.unwrap();
        assert!(
            cache
                .memories(&mut conn, OWNER, start, end, None, "")
                .await
                .unwrap()
                .is_none(),
            "{change}"
        );
        sqlx::query("ROLLBACK").execute(&mut *conn).await.unwrap();
        compare(&cache, &mut conn, None, "").await;
    }
    // Changes to another owner's period do not invalidate this owner.
    sqlx::query("INSERT INTO astra_initial_history_dirty VALUES($1,'2026-09-01')")
        .bind(OTHER)
        .execute(&mut *conn)
        .await
        .unwrap();
    compare(&cache, &mut conn, None, "").await;
    // An already-existing post-boundary comment must force fallback after a
    // migration/restart, even if reset removes it without firing triggers.
    sqlx::raw_sql("SET session_replication_role=replica; INSERT INTO comments VALUES(gen_random_uuid()::text,(SELECT min(id) FROM posts),'00000000-0000-0000-0000-000000000002','recent',now(),NULL); SET session_replication_role=origin").execute(&mut *conn).await.unwrap();
    sqlx::raw_sql(migration).execute(&mut *conn).await.unwrap();
    let restarted = InitialHistory::load(&db, "asia").await.unwrap();
    assert!(!restarted.can_export(OWNER));
    sqlx::raw_sql("SET session_replication_role=replica; DELETE FROM comments WHERE created_at>(SELECT created_at FROM initial_data_boundary); SET session_replication_role=origin").execute(&mut *conn).await.unwrap();
    assert!(
        restarted
            .memories(&mut conn, OWNER, start, end, None, "")
            .await
            .unwrap()
            .is_none()
    );
    assert!(
        cache
            .memories(&mut conn, OWNER, start, end, None, "")
            .await
            .unwrap()
            .is_none()
    );
    // Recent comments dirty Memories, but must not evict export metadata.
    assert!(cache.export(&mut conn, OWNER).await.unwrap().is_some());
    let new_image = uuid::Uuid::new_v4().to_string();
    let key = files
        .put(&new_image, &Bytes::from_static(b"new-image"))
        .await
        .unwrap();
    sqlx::query("INSERT INTO image_objects VALUES($1,now(),'',$2)")
        .bind(&new_image)
        .bind(key)
        .execute(&mut *conn)
        .await
        .unwrap();
    let new_post = uuid::Uuid::new_v4().to_string();
    sqlx::query("INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at,created_at) VALUES($1,$2,'00000000-0000-0000-0000-000000000010',$3,'2026-09-01 10:00:15Z',now())").bind(&new_post).bind(OWNER).bind(&new_image).execute(&mut *conn).await.unwrap();
    assert!(cache.export(&mut conn, OWNER).await.unwrap().is_some());
    let combined = export_contents(&cached_app).await;
    assert_eq!(combined.0.as_array().unwrap().len(), 56);
    assert_eq!(combined, export_contents(&sql_app).await);
    sqlx::query("UPDATE posts SET caption='new caption' WHERE id=$1")
        .bind(&new_post)
        .execute(&mut *conn)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE users SET display_name='Export author',secret_key_hash=repeat('ef',32) WHERE id=$1",
    )
    .bind(OWNER)
    .execute(&mut *conn)
    .await
    .unwrap();
    assert!(cache.export(&mut conn, OWNER).await.unwrap().is_some());
    assert_eq!(
        export_contents(&cached_app).await,
        export_contents(&sql_app).await
    );
    sqlx::query("UPDATE image_objects SET asset_key=NULL,data='new-inline-bytes' WHERE id=$1")
        .bind(&new_image)
        .execute(&mut *conn)
        .await
        .unwrap();
    assert!(cache.export(&mut conn, OWNER).await.unwrap().is_some());
    let fresh_image = export_contents(&cached_app).await;
    assert!(
        fresh_image
            .1
            .iter()
            .any(|bytes| bytes == b"new-inline-bytes")
    );
    assert_eq!(fresh_image, export_contents(&sql_app).await);
    // Reset omits triggers; fresh delta rows disappear, initial rows survive.
    sqlx::raw_sql("SET session_replication_role=replica; DELETE FROM posts WHERE created_at>(SELECT created_at FROM initial_data_boundary); DELETE FROM image_objects WHERE created_at>(SELECT created_at FROM initial_data_boundary); SET session_replication_role=origin").execute(&mut *conn).await.unwrap();
    assert_eq!(
        export_contents(&cached_app).await,
        export_contents(&sql_app).await
    );
    assert_eq!(
        export_contents(&cached_app)
            .await
            .0
            .as_array()
            .unwrap()
            .len(),
        55
    );
    // Invalidation of initial metadata/bytes is transactional and conservative.
    for change in [
        "UPDATE posts SET caption='initial edit' WHERE id=(SELECT min(id) FROM posts)",
        "UPDATE image_objects SET asset_key=NULL,data='changed' WHERE id=(SELECT min(image_id) FROM posts)",
        "DELETE FROM image_objects WHERE id=(SELECT min(image_id) FROM posts)",
        "INSERT INTO image_objects SELECT id,created_at+interval '1 second',data,asset_key FROM image_objects LIMIT 1",
        "TRUNCATE image_objects",
    ] {
        sqlx::query("BEGIN").execute(&mut *conn).await.unwrap();
        sqlx::raw_sql(change).execute(&mut *conn).await.unwrap();
        assert!(
            cache.export(&mut conn, OWNER).await.unwrap().is_none(),
            "{change}"
        );
        sqlx::query("ROLLBACK").execute(&mut *conn).await.unwrap();
        assert!(cache.export(&mut conn, OWNER).await.unwrap().is_some());
    }
    sqlx::query("UPDATE image_objects SET asset_key=NULL,data='changed' WHERE id=(SELECT min(image_id) FROM posts)").execute(&mut *conn).await.unwrap();
    assert!(cache.export(&mut conn, OWNER).await.unwrap().is_none());
    assert_eq!(
        export_contents(&cached_app).await,
        export_contents(&sql_app).await
    );
    std::fs::remove_dir_all(root).unwrap();
    drop(conn);
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
}
