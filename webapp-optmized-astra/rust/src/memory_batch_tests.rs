use super::*;
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::str::FromStr;

async fn compare(
    db: &PgPool,
    owner: &str,
    start: NaiveDate,
    end: NaiveDate,
    before: Option<DateTime<Utc>>,
    id: &str,
) -> serde_json::Value {
    let actual = super::list_memories(db, owner, start, end, before, id)
        .await
        .unwrap();
    let expected = crate::memory_api::list_memories(
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
async fn batched_memory_matches_reference_visibility_limits_and_pagination() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_memory_batch_{}", uuid::Uuid::new_v4().simple());
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
    sqlx::raw_sql(include_str!("../tests/fixtures/memory.sql"))
        .execute(&db)
        .await
        .unwrap();
    // Forward deployment reruns all migrations: the retired cache must leave
    // no write-side trigger or ledger behind, including on image partitions.
    for _ in 0..2 {
        sqlx::raw_sql(include_str!("../../sql/018-history-cache.sql"))
            .execute(&db)
            .await
            .unwrap();
        sqlx::raw_sql(include_str!("../../sql/019-retire-history-cache.sql"))
            .execute(&db)
            .await
            .unwrap();
    }
    assert!(sqlx::query_scalar::<_,bool>("SELECT to_regclass('astra_history_changes') IS NULL AND to_regclass('astra_history_epoch') IS NULL")
        .fetch_one(&db).await.unwrap());
    let start = NaiveDate::from_ymd_opt(2026, 1, 1).unwrap();
    let end = NaiveDate::from_ymd_opt(2026, 2, 1).unwrap();
    let first = compare(&db, "owner", start, end, None, "").await;
    assert_eq!(first["items"].as_array().unwrap().len(), 50);
    assert_eq!(first["items"][0]["comments"].as_array().unwrap().len(), 50);
    assert_eq!(first["items"][0]["realmojis"].as_array().unwrap().len(), 50);
    assert_eq!(first["hasMore"], true);
    let cursor = DateTime::parse_from_rfc3339(first["items"][49]["postedAt"].as_str().unwrap())
        .unwrap()
        .to_utc();
    let next = compare(
        &db,
        "owner",
        start,
        end,
        Some(cursor),
        first["items"][49]["id"].as_str().unwrap(),
    )
    .await;
    assert_eq!(next["items"].as_array().unwrap().len(), 5);
    assert_eq!(next["hasMore"], false);
    assert_eq!(
        compare(&db, "absent", start, end, None, "").await["items"],
        serde_json::json!([])
    );
    assert_eq!(
        compare(
            &db,
            "owner",
            end,
            NaiveDate::from_ymd_opt(2026, 3, 1).unwrap(),
            None,
            ""
        )
        .await["items"],
        serde_json::json!([])
    );
    compare(&db, "owner", start, start + chrono::Days::new(1), None, "").await;
    assert!(
        super::list_memories(
            &db,
            "owner",
            start,
            end,
            None,
            "cursor-without-time"
        )
        .await
        .is_err()
    );
    sqlx::raw_sql("INSERT INTO users(id,username,secret_key_hash) VALUES('other','other',repeat('ef',32)); INSERT INTO comments SELECT 'other-c'||n,'p055','other','body','2026-01-01T01:00Z'::timestamptz+n*interval '1 second',NULL FROM generate_series(1,55) n; INSERT INTO realmojis SELECT 'other-r'||n,'p055','other','other-image'||n,'smile','2025-12-31T23:00Z'::timestamptz+n*interval '1 second',NULL,NULL FROM generate_series(1,55) n;")
        .execute(&db).await.unwrap();
    sqlx::query("UPDATE users SET status='deleted' WHERE id='friend'")
        .execute(&db)
        .await
        .unwrap();
    let filtered = compare(&db, "owner", start, end, None, "").await;
    // Inactive users must be filtered before each interaction's LIMIT 50.
    assert_eq!(
        filtered["items"][0]["comments"].as_array().unwrap().len(),
        50
    );
    assert_eq!(
        filtered["items"][0]["realmojis"].as_array().unwrap().len(),
        50
    );
    for query in [
        "UPDATE users SET status='active',display_name='changed',secret_key_hash=repeat('ef',32) WHERE id='friend'",
        "UPDATE comments SET deleted_at=clock_timestamp() WHERE id='c1'",
        "UPDATE realmojis SET replaced_at=clock_timestamp() WHERE id='r55'",
        "INSERT INTO comments VALUES('new','p055','friend','new','2026-01-01T00:00Z',NULL)",
        "UPDATE posts SET caption='changed' WHERE id='p055'",
        "UPDATE posting_windows SET ended_at=clock_timestamp(),on_time_deadline=on_time_deadline-interval '1 hour'",
        "UPDATE posts SET deleted_at=clock_timestamp() WHERE id='p055'",
        "UPDATE users SET status='deleted' WHERE id='owner'",
    ] {
        sqlx::query(query).execute(&db).await.unwrap();
        compare(&db, "owner", start, end, None, "").await;
    }
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
}
