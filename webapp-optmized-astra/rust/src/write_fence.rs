//! Wait for global write visibility after committing, outside the DB transaction.
use axum::extract::{Request, State};
use axum::http::{Method, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use crate::api_error::new_api_error;
use crate::logger;
use crate::read_fence::ReadFence;

#[derive(Clone)]
pub struct WriteFence(Vec<ReadFence>);

impl WriteFence {
    pub fn new(replicas: Vec<ReadFence>) -> Self {
        assert_eq!(replicas.len(), 2, "both regional replicas must be fenced");
        Self(replicas)
    }

    pub async fn wait(&self) -> Result<(), String> {
        futures_util::future::try_join_all(self.0.iter().map(ReadFence::wait)).await?;
        Ok(())
    }
}

pub async fn write_fence_middleware(
    State(fence): State<Option<WriteFence>>,
    req: Request,
    next: Next,
) -> Response {
    let mutation = !matches!(*req.method(), Method::GET | Method::HEAD);
    let response = next.run(req).await;
    // Capture WAL only after the handler has completed its commits. Failed
    // responses are fenced as well, since a handler may have committed work.
    // The outer run_to_completion layer also finishes this after disconnects.
    if mutation
        && let Some(fence) = fence
        && let Err(err) = fence.wait().await
    {
        logger::error("failed to make write visible on replicas", &err);
        return new_api_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "replica_visibility_unavailable",
        )
        .into_response();
    }
    response
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        Router,
        body::Body,
        http::Request as HttpRequest,
        middleware::{from_fn, from_fn_with_state},
        routing::{get, post},
    };
    use futures_util::FutureExt;
    use sqlx::PgPool;
    use std::panic::AssertUnwindSafe;
    use std::time::Duration;
    use tokio::time::{sleep, timeout};
    use tower::ServiceExt;

    fn app(primary: PgPool, table: String, fence: WriteFence) -> Router {
        Router::new()
            .route(
                "/write",
                post(move || {
                    let primary = primary.clone();
                    let table = table.clone();
                    async move {
                        sqlx::query(&format!("UPDATE {table} SET value=value+1"))
                            .execute(&primary)
                            .await
                            .unwrap();
                        StatusCode::NO_CONTENT
                    }
                }),
            )
            .route("/read", get(|| async { StatusCode::OK }))
            .layer(from_fn_with_state(Some(fence), write_fence_middleware))
            .layer(from_fn(crate::server::run_to_completion))
    }

    async fn value(db: &PgPool, table: &str) -> i32 {
        sqlx::query_scalar(&format!("SELECT value FROM {table}"))
            .fetch_one(db)
            .await
            .unwrap()
    }

    async fn wait_value(db: &PgPool, table: &str, expected: i32) {
        timeout(Duration::from_secs(2), async {
            while value(db, table).await != expected {
                sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn responses_wait_for_both_replicas_after_releasing_write_locks() {
        let (Ok(primary_dsn), Ok(replica_dsn), Ok(replica2_dsn)) = (
            std::env::var("ISUREAL_FENCE_PRIMARY_DSN"),
            std::env::var("ISUREAL_FENCE_REPLICA_DSN"),
            std::env::var("ISUREAL_FENCE_REPLICA2_DSN"),
        ) else {
            return;
        };
        let _serial = crate::read_fence::TEST_REPLICA_LOCK.lock().await;
        let primary = PgPool::connect(&primary_dsn).await.unwrap();
        let replica = PgPool::connect(&replica_dsn).await.unwrap();
        let replica2 = PgPool::connect(&replica2_dsn).await.unwrap();
        assert!(
            sqlx::query_scalar::<_, bool>("SELECT pg_is_in_recovery()")
                .fetch_one(&replica)
                .await
                .unwrap()
        );
        assert!(
            sqlx::query_scalar::<_, bool>("SELECT pg_is_in_recovery()")
                .fetch_one(&replica2)
                .await
                .unwrap()
        );
        let table = format!("astra_write_fence_{}", uuid::Uuid::new_v4().simple());
        let fence = WriteFence::new(vec![
            ReadFence::new(primary.clone(), replica.clone(), Duration::from_secs(3)),
            ReadFence::new(primary.clone(), replica2.clone(), Duration::from_secs(3)),
        ]);
        let result = AssertUnwindSafe(async {
            sqlx::raw_sql(&format!(
                "CREATE TABLE {table} (value integer); INSERT INTO {table} VALUES (0);"
            ))
            .execute(&primary)
            .await
            .unwrap();
            fence.wait().await.unwrap();
            let app = app(primary.clone(), table.clone(), fence.clone());
            sqlx::query("SELECT pg_wal_replay_pause()")
                .execute(&replica2)
                .await
                .unwrap();
            timeout(Duration::from_secs(2), async {
                while !sqlx::query_scalar::<_, bool>(
                    "SELECT pg_get_wal_replay_pause_state()='paused'",
                )
                .fetch_one(&replica2)
                .await
                .unwrap()
                {
                    sleep(Duration::from_millis(5)).await;
                }
            })
            .await
            .unwrap();
            let mut first = tokio::spawn(
                app.clone().oneshot(
                    HttpRequest::builder()
                        .method("POST")
                        .uri("/write")
                        .body(Body::empty())
                        .unwrap(),
                ),
            );
            wait_value(&primary, &table, 1).await;
            assert!(
                timeout(Duration::from_millis(100), &mut first)
                    .await
                    .is_err()
            );
            assert_eq!(value(&replica2, &table).await, 0);
            // The first HTTP response is pending but its transaction lock must
            // already be released, allowing an independent update to commit.
            let mut tx = primary.begin().await.unwrap();
            sqlx::query("SET LOCAL lock_timeout='200ms'")
                .execute(&mut *tx)
                .await
                .unwrap();
            sqlx::query(&format!("UPDATE {table} SET value=value+1"))
                .execute(&mut *tx)
                .await
                .unwrap();
            tx.commit().await.unwrap();
            let mut second = tokio::spawn(
                app.clone().oneshot(
                    HttpRequest::builder()
                        .method("POST")
                        .uri("/write")
                        .body(Body::empty())
                        .unwrap(),
                ),
            );
            wait_value(&primary, &table, 3).await;
            assert!(
                timeout(Duration::from_millis(100), &mut second)
                    .await
                    .is_err()
            );
            // GET has no additional regional barrier in this mode.
            assert_eq!(
                timeout(
                    Duration::from_millis(100),
                    app.clone().oneshot(
                        HttpRequest::builder()
                            .uri("/read")
                            .body(Body::empty())
                            .unwrap()
                    )
                )
                .await
                .unwrap()
                .unwrap()
                .status(),
                StatusCode::OK
            );
            sqlx::query("SELECT pg_wal_replay_resume()")
                .execute(&replica2)
                .await
                .unwrap();
            assert_eq!(
                timeout(Duration::from_secs(3), first)
                    .await
                    .unwrap()
                    .unwrap()
                    .unwrap()
                    .status(),
                StatusCode::NO_CONTENT
            );
            assert_eq!(
                timeout(Duration::from_secs(3), second)
                    .await
                    .unwrap()
                    .unwrap()
                    .unwrap()
                    .status(),
                StatusCode::NO_CONTENT
            );
            assert_eq!(value(&replica, &table).await, 3);
            assert_eq!(value(&replica2, &table).await, 3);
        })
        .catch_unwind()
        .await;
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&replica2)
            .await
            .unwrap();
        sqlx::query(&format!("DROP TABLE IF EXISTS {table}"))
            .execute(&primary)
            .await
            .unwrap();
        primary.close().await;
        replica.close().await;
        replica2.close().await;
        if let Err(panic) = result {
            std::panic::resume_unwind(panic);
        }
    }
}
