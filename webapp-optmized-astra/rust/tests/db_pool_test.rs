use sqlx::PgPool;
use std::time::Duration;

#[tokio::test]
async fn idle_dead_connections_are_replaced_and_transactions_remain_fresh() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let pool = isureal::db_pool::options(1).connect(&dsn).await.unwrap();
    let original: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&pool)
        .await
        .unwrap();
    tokio::time::sleep(Duration::from_millis(20)).await;
    let terminated: bool = sqlx::query_scalar("SELECT pg_terminate_backend($1)")
        .bind(original)
        .fetch_one(&admin)
        .await
        .unwrap();
    assert!(terminated);
    tokio::time::sleep(Duration::from_millis(1100)).await;
    let replacement: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_ne!(replacement, original);
    let table = format!("pool_test_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE TABLE {table}(value integer)"))
        .execute(&admin)
        .await
        .unwrap();
    sqlx::query(&format!("INSERT INTO {table} VALUES (1)"))
        .execute(&admin)
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query(&format!("UPDATE {table} SET value=99"))
        .execute(&mut *tx)
        .await
        .unwrap();
    drop(tx);
    assert_eq!(
        sqlx::query_scalar::<_, i32>(&format!("SELECT value FROM {table}"))
            .fetch_one(&pool)
            .await
            .unwrap(),
        1
    );
    sqlx::query(&format!("UPDATE {table} SET value=2"))
        .execute(&admin)
        .await
        .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, i32>(&format!("SELECT value FROM {table}"))
            .fetch_one(&pool)
            .await
            .unwrap(),
        2
    );
    sqlx::query(&format!("DROP TABLE {table}"))
        .execute(&admin)
        .await
        .unwrap();
    pool.close().await;
    admin.close().await;
}
