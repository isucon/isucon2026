use super::*;
use crate::memory_api::{cached_memories, list_memories};
use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
use std::str::FromStr;

async fn assert_matches(
    db: &PgPool,
    owner: &str,
    before: Option<chrono::DateTime<chrono::Utc>>,
    id: &str,
) -> serde_json::Value {
    let start = NaiveDate::from_ymd_opt(2026, 1, 1).unwrap();
    let end = NaiveDate::from_ymd_opt(2026, 2, 1).unwrap();
    let actual = cached_memories(db, owner, start, end, before, id)
        .await
        .unwrap();
    let expected = list_memories(
        &mut db.acquire().await.unwrap(),
        owner,
        start,
        end,
        before,
        id,
    )
    .await
    .unwrap();
    let actual = serde_json::to_value(actual).unwrap();
    assert_eq!(actual, serde_json::to_value(expected).unwrap());
    actual
}

#[tokio::test]
async fn cached_records_follow_commits_user_changes_pagination_and_reset() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_history_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(6)
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
        CREATE TABLE users(id text PRIMARY KEY,username text NOT NULL,secret_key_hash text NOT NULL,
          display_name text NOT NULL DEFAULT '',bio text NOT NULL DEFAULT '',profile_image_id text,
          region text NOT NULL DEFAULT 'asia',account_type text NOT NULL DEFAULT 'normal',status text NOT NULL DEFAULT 'active');
        CREATE TABLE posting_windows(id text PRIMARY KEY,local_date date NOT NULL,on_time_deadline timestamptz NOT NULL,ended_at timestamptz);
        CREATE TABLE posts(id text PRIMARY KEY,author_id text NOT NULL,posting_window_id text NOT NULL,image_id text NOT NULL,
          caption text NOT NULL DEFAULT '',visibility text NOT NULL DEFAULT 'public',location_kind text NOT NULL DEFAULT 'none',
          latitude numeric,longitude numeric,locality text,posted_at timestamptz NOT NULL,deleted_at timestamptz);
        CREATE INDEX ON posts(author_id);
        CREATE TABLE comments(id text PRIMARY KEY,post_id text,author_id text,body text,created_at timestamptz,deleted_at timestamptz);
        CREATE TABLE realmojis(id text PRIMARY KEY,post_id text,reactor_id text,image_id text,reaction_type text,created_at timestamptz,replaced_at timestamptz,deleted_at timestamptz);
        CREATE TABLE image_objects(id text,owner_user_id text,data bytea,asset_key text,created_at timestamptz,
          PRIMARY KEY(id,created_at)) PARTITION BY RANGE(created_at);
        CREATE TABLE image_objects_202601 PARTITION OF image_objects FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
        INSERT INTO users(id,username,secret_key_hash) VALUES('owner','owner',repeat('ab',32)),('friend','friend',repeat('cd',32));
        INSERT INTO posting_windows VALUES('w','2026-01-01','2026-01-01T01:00Z',NULL);
        INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at)
          SELECT 'p'||lpad(n::text,3,'0'),'owner','w','i'||n,'2026-01-01T00:00Z'::timestamptz+n*interval '1 minute' FROM generate_series(1,55) n;
        INSERT INTO image_objects SELECT image_id,'owner',''::bytea,'pack:original:'||image_id,posted_at FROM posts;
        INSERT INTO comments SELECT 'c'||n,'p055','friend','body'||n,'2026-01-01T00:00Z'::timestamptz+n*interval '1 second',NULL FROM generate_series(1,55) n;
        INSERT INTO realmojis SELECT 'r'||n,'p055','friend','rimg'||n,'smile','2026-01-01T00:00Z'::timestamptz+n*interval '1 second',NULL,NULL FROM generate_series(1,55) n;
    "#).execute(&db).await.unwrap();
    sqlx::raw_sql(include_str!("../../sql/018-history-cache.sql"))
        .execute(&db)
        .await
        .unwrap();
    // Migration reruns preserve versions and work with image partitions.
    sqlx::raw_sql(include_str!("../../sql/018-history-cache.sql"))
        .execute(&db)
        .await
        .unwrap();
    let first = assert_matches(&db, "owner", None, "").await;
    assert_eq!(first["items"].as_array().unwrap().len(), 50);
    assert_eq!(first["items"][0]["comments"].as_array().unwrap().len(), 50);
    assert_eq!(first["items"][0]["realmojis"].as_array().unwrap().len(), 50);
    assert_eq!(first["hasMore"], true);
    let snapshot = load(&db, "owner").await.unwrap();
    assert!(Arc::ptr_eq(&snapshot, &load(&db, "owner").await.unwrap()));
    let at = chrono::DateTime::parse_from_rfc3339(first["items"][49]["postedAt"].as_str().unwrap())
        .unwrap()
        .to_utc();
    let next = assert_matches(
        &db,
        "owner",
        Some(at),
        first["items"][49]["id"].as_str().unwrap(),
    )
    .await;
    assert_eq!(next["items"].as_array().unwrap().len(), 5);
    assert_eq!(next["hasMore"], false);
    // The single-statement loader must retain a revision even for no rows,
    // and an empty cached history must not hide the first subsequent post.
    assert_eq!(
        assert_matches(&db, "absent", None, "").await["items"],
        serde_json::json!([])
    );
    sqlx::raw_sql("INSERT INTO users(id,username,secret_key_hash) VALUES('absent','absent',repeat('ab',32)); INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at) VALUES('new-owner-post','absent','w','new-owner-image','2026-01-01T00:01Z'); INSERT INTO image_objects VALUES('new-owner-image','absent',''::bytea,'pack:new-owner-image','2026-01-01T00:01Z')")
        .execute(&db).await.unwrap();
    assert_eq!(
        assert_matches(&db, "absent", None, "").await["items"][0]["id"],
        "new-owner-post"
    );
    // Fresh user fields and keys are rendered even with the same cached records.
    sqlx::query(
        "UPDATE users SET display_name='changed',secret_key_hash=repeat('ef',32) WHERE id='friend'",
    )
    .execute(&db)
    .await
    .unwrap();
    assert_matches(&db, "owner", None, "").await;
    assert!(Arc::ptr_eq(&snapshot, &load(&db, "owner").await.unwrap()));
    sqlx::query("UPDATE users SET status='deleted' WHERE id='friend'")
        .execute(&db)
        .await
        .unwrap();
    let hidden = assert_matches(&db, "owner", None, "").await;
    assert_eq!(hidden["items"][0]["comments"], serde_json::json!([]));
    assert_eq!(hidden["items"][0]["realmojis"], serde_json::json!([]));
    sqlx::query("UPDATE users SET status='active' WHERE id='friend'")
        .execute(&db)
        .await
        .unwrap();
    // Opening/closing windows does not change historical response fields.
    sqlx::raw_sql("UPDATE posting_windows SET ended_at=clock_timestamp(); INSERT INTO posting_windows VALUES('new-window','2026-01-02','2026-01-02T01:00Z',NULL)")
        .execute(&db).await.unwrap();
    assert_matches(&db, "owner", None, "").await;
    assert!(Arc::ptr_eq(&snapshot, &load(&db, "owner").await.unwrap()));
    for query in [
        "UPDATE comments SET deleted_at=clock_timestamp() WHERE id='c1'",
        "UPDATE realmojis SET replaced_at=clock_timestamp() WHERE id='r55'",
        "INSERT INTO comments VALUES('new','p055','friend','new','2026-01-01T00:00Z',NULL)",
        "UPDATE posts SET caption='changed' WHERE id='p055'",
        "UPDATE posting_windows SET on_time_deadline=on_time_deadline-interval '1 hour'",
        "UPDATE posts SET deleted_at=clock_timestamp() WHERE id='p055'",
    ] {
        let old = load(&db, "owner").await.unwrap();
        sqlx::query(query).execute(&db).await.unwrap();
        assert_matches(&db, "owner", None, "").await;
        assert!(!Arc::ptr_eq(&old, &load(&db, "owner").await.unwrap()));
    }
    // Export metadata hits retain ordering, and asset changes invalidate them.
    let cached = crate::image_api::cached_export_images("owner");
    let rows = crate::export_api::export_rows(&db, "owner", &cached)
        .await
        .unwrap();
    let again = crate::export_api::export_rows(&db, "owner", &cached)
        .await
        .unwrap();
    assert_eq!(rows.len(), 54);
    assert_eq!(
        rows.iter().map(|p| &p.post.id).collect::<Vec<_>>(),
        again.iter().map(|p| &p.post.id).collect::<Vec<_>>()
    );
    sqlx::query("UPDATE image_objects SET asset_key='pack:changed' WHERE id='i1'")
        .execute(&db)
        .await
        .unwrap();
    let changed = crate::export_api::export_rows(&db, "owner", &cached)
        .await
        .unwrap();
    assert_eq!(changed[0].asset_key.as_deref(), Some("pack:changed"));
    // Out-of-order commit must invalidate twice, unlike a MAX(transaction_id) token.
    let mut older = db.begin().await.unwrap();
    sqlx::query("UPDATE posts SET caption='older' WHERE id='p001'")
        .execute(&mut *older)
        .await
        .unwrap();
    let mut newer = db.begin().await.unwrap();
    sqlx::query("UPDATE posts SET caption='newer' WHERE id='p002'")
        .execute(&mut *newer)
        .await
        .unwrap();
    newer.commit().await.unwrap();
    let middle = load(&db, "owner").await.unwrap();
    older.commit().await.unwrap();
    assert!(!Arc::ptr_eq(&middle, &load(&db, "owner").await.unwrap()));
    assert_matches(&db, "owner", None, "").await;
    let mut aborted = db.begin().await.unwrap();
    sqlx::query("UPDATE posts SET caption='aborted' WHERE id='p001'")
        .execute(&mut *aborted)
        .await
        .unwrap();
    let before_rollback = load(&db, "owner").await.unwrap();
    aborted.rollback().await.unwrap();
    assert!(Arc::ptr_eq(
        &before_rollback,
        &load(&db, "owner").await.unwrap()
    ));
    // initialize disables triggers, so the explicit epoch change is essential.
    sqlx::raw_sql("BEGIN; SET LOCAL session_replication_role=replica; UPDATE astra_history_epoch SET epoch=gen_random_uuid()::text; TRUNCATE astra_history_changes; UPDATE posts SET caption='reset'; COMMIT").execute(&db).await.unwrap();
    assert!(!Arc::ptr_eq(
        &before_rollback,
        &load(&db, "owner").await.unwrap()
    ));
    assert_matches(&db, "owner", None, "").await;
    sqlx::query("UPDATE users SET status='deleted' WHERE id='owner'")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(
        assert_matches(&db, "owner", None, "").await["items"],
        serde_json::json!([])
    );
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
}

#[test]
fn cache_enforces_bytes_and_revision() {
    let rev = Revision {
        epoch: "epoch".into(),
        global_changes: 0,
        owner_changes: 0,
    };
    let mut cache = Cache::new(10);
    cache.insert("a", rev.clone(), Arc::new(1), 6);
    cache.insert("b", rev.clone(), Arc::new(2), 6);
    assert!(cache.get("a", &rev).is_none());
    assert_eq!(*cache.get("b", &rev).unwrap(), 2);
    let mut changed = rev.clone();
    changed.owner_changes += 1;
    assert!(cache.get("b", &changed).is_none());
    cache.insert("b", rev.clone(), Arc::new(3), 11);
    assert!(cache.get("b", &rev).is_none());
    assert_eq!(cache.bytes, 0);
}
