use chrono::{DateTime, NaiveDate, Utc};
use isureal::{initial_history::InitialHistory, memory_api::list_memories};
use sqlx::{
    PgConnection, PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::str::FromStr;

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
    let migration = include_str!("../../sql/020-initial-history.sql");
    sqlx::raw_sql(migration).execute(&db).await.unwrap();
    let cache = InitialHistory::load(&db, "asia").await.unwrap();
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
    drop(conn);
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
}
