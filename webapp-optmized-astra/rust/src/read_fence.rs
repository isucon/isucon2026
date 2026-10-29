//! A shared barrier for fresh reads from a physical PostgreSQL standby.
//! Each batch captures a durable WAL position after all its requests arrived,
//! then waits for the local standby to replay at least that position.
use std::sync::{Arc, Mutex};
use std::time::Duration;

use crate::{api_error::new_api_error, logger};
use axum::extract::{Request, State};
use axum::http::StatusCode;
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};
use sqlx::PgPool;
use tokio::sync::oneshot;

#[derive(Default)]
struct Pending {
    running: bool,
    waiters: Vec<oneshot::Sender<Result<(), String>>>,
}

struct Inner {
    primary: PgPool,
    replica: PgPool,
    timeout: Duration,
    pending: Mutex<Pending>,
}

#[derive(Clone)]
pub struct ReadFence(Arc<Inner>);

impl ReadFence {
    pub fn new(primary: PgPool, replica: PgPool, timeout: Duration) -> Self {
        Self(Arc::new(Inner {
            primary,
            replica,
            timeout,
            pending: Mutex::default(),
        }))
    }

    pub async fn wait(&self) -> Result<(), String> {
        let (sender, receiver) = oneshot::channel();
        {
            let mut pending = self.0.pending.lock().unwrap();
            pending.waiters.push(sender);
            if !pending.running {
                pending.running = true;
                let fence = self.clone();
                tokio::spawn(async move { fence.run().await });
            }
        }
        receiver
            .await
            .map_err(|_| "read fence worker stopped".to_owned())?
    }

    async fn run(self) {
        loop {
            let waiters = {
                let mut pending = self.0.pending.lock().unwrap();
                pending.waiters.retain(|waiter| !waiter.is_closed());
                if pending.waiters.is_empty() {
                    pending.running = false;
                    return;
                }
                std::mem::take(&mut pending.waiters)
            };
            // Requests arriving after this batch was taken must use a new WAL
            // sample, even if the preceding batch happened to reach its target.
            let result = tokio::time::timeout(self.0.timeout, self.synchronize())
                .await
                .unwrap_or_else(|_| Err("read replica catch-up timed out".to_owned()));
            for waiter in waiters {
                let _ = waiter.send(result.clone());
            }
        }
    }

    async fn synchronize(&self) -> Result<(), String> {
        let target: String = sqlx::query_scalar("SELECT pg_current_wal_flush_lsn()::text")
            .fetch_one(&self.0.primary)
            .await
            .map_err(|err| format!("capture primary WAL position: {err}"))?;
        let mut delay = Duration::from_millis(2);
        loop {
            let replayed: bool = sqlx::query_scalar(
                "SELECT COALESCE(pg_last_wal_replay_lsn() >= $1::text::pg_lsn, false)",
            )
            .bind(&target)
            .fetch_one(&self.0.replica)
            .await
            .map_err(|err| format!("check replica WAL position: {err}"))?;
            if replayed {
                return Ok(());
            }
            tokio::time::sleep(delay).await;
            delay = (delay * 2).min(Duration::from_millis(20));
        }
    }
}

pub async fn read_fence_middleware(
    State(fence): State<Option<ReadFence>>,
    req: Request,
    next: Next,
) -> Response {
    // Even mutation requests can consult the regional IP-ban list before
    // reaching their primary-backed handler when HTTP write forwarding is off.
    if let Some(fence) = fence
        && let Err(err) = fence.wait().await
    {
        logger::error("failed to synchronize read replica", &err);
        return new_api_error(StatusCode::SERVICE_UNAVAILABLE, "read_replica_unavailable")
            .into_response();
    }
    next.run(req).await
}

#[cfg(test)]
pub(crate) static TEST_REPLICA_LOCK: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

#[cfg(test)]
mod tests {
    use super::*;
    use futures_util::FutureExt;
    use std::panic::AssertUnwindSafe;
    use tokio::time::timeout;

    async fn pause(replica: &PgPool) {
        sqlx::query("SELECT pg_wal_replay_pause()")
            .execute(replica)
            .await
            .unwrap();
        timeout(Duration::from_secs(2), async {
            loop {
                let paused: bool =
                    sqlx::query_scalar("SELECT pg_get_wal_replay_pause_state() = 'paused'")
                        .fetch_one(replica)
                        .await
                        .unwrap();
                if paused {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn physical_replica_barrier_waits_for_new_commits_and_fails_closed() {
        let (Ok(primary_dsn), Ok(replica_dsn)) = (
            std::env::var("ISUREAL_FENCE_PRIMARY_DSN"),
            std::env::var("ISUREAL_FENCE_REPLICA_DSN"),
        ) else {
            return;
        };
        let _serial = TEST_REPLICA_LOCK.lock().await;
        let primary = PgPool::connect(&primary_dsn).await.unwrap();
        let replica = PgPool::connect(&replica_dsn).await.unwrap();
        assert!(
            !sqlx::query_scalar::<_, bool>("SELECT pg_is_in_recovery()")
                .fetch_one(&primary)
                .await
                .unwrap()
        );
        assert!(
            sqlx::query_scalar::<_, bool>("SELECT pg_is_in_recovery()")
                .fetch_one(&replica)
                .await
                .unwrap()
        );
        let table = format!("astra_fence_{}", uuid::Uuid::new_v4().simple());
        let result = AssertUnwindSafe(async {
            sqlx::raw_sql(&format!(
                "CREATE TABLE {table} (value integer); INSERT INTO {table} VALUES (1);"
            ))
            .execute(&primary)
            .await
            .unwrap();
            let fence = ReadFence::new(primary.clone(), replica.clone(), Duration::from_secs(3));
            fence.wait().await.unwrap();
            assert_eq!(
                sqlx::query_scalar::<_, i32>(&format!("SELECT value FROM {table}"))
                    .fetch_one(&replica)
                    .await
                    .unwrap(),
                1
            );
            pause(&replica).await;
            sqlx::query(&format!("UPDATE {table} SET value=2"))
                .execute(&primary)
                .await
                .unwrap();
            let mut waiters = tokio::task::JoinSet::new();
            for _ in 0..32 {
                let fence = fence.clone();
                waiters.spawn(async move { fence.wait().await });
            }
            let cancelled = {
                let fence = fence.clone();
                tokio::spawn(async move { fence.wait().await })
            };
            cancelled.abort();
            assert!(
                timeout(Duration::from_millis(120), waiters.join_next())
                    .await
                    .is_err()
            );
            sqlx::query("SELECT pg_wal_replay_resume()")
                .execute(&replica)
                .await
                .unwrap();
            timeout(Duration::from_secs(3), async {
                while let Some(result) = waiters.join_next().await {
                    result.unwrap().unwrap();
                }
            })
            .await
            .unwrap();
            assert_eq!(
                sqlx::query_scalar::<_, i32>(&format!("SELECT value FROM {table}"))
                    .fetch_one(&replica)
                    .await
                    .unwrap(),
                2
            );
            // A previous successful fence cannot satisfy a request after a new
            // primary commit while replication is paused again.
            pause(&replica).await;
            sqlx::query(&format!("UPDATE {table} SET value=3"))
                .execute(&primary)
                .await
                .unwrap();
            let mut waiting = {
                let fence = fence.clone();
                tokio::spawn(async move { fence.wait().await })
            };
            assert!(
                timeout(Duration::from_millis(120), &mut waiting)
                    .await
                    .is_err()
            );
            sqlx::query("SELECT pg_wal_replay_resume()")
                .execute(&replica)
                .await
                .unwrap();
            timeout(Duration::from_secs(3), waiting)
                .await
                .unwrap()
                .unwrap()
                .unwrap();
            assert_eq!(
                sqlx::query_scalar::<_, i32>(&format!("SELECT value FROM {table}"))
                    .fetch_one(&replica)
                    .await
                    .unwrap(),
                3
            );
            // Timeout must reject the read, then allow later recovery.
            pause(&replica).await;
            sqlx::query(&format!("UPDATE {table} SET value=4"))
                .execute(&primary)
                .await
                .unwrap();
            let short =
                ReadFence::new(primary.clone(), replica.clone(), Duration::from_millis(180));
            assert!(short.wait().await.unwrap_err().contains("timed out"));
            use axum::{
                Router,
                body::Body,
                http::Request as HttpRequest,
                middleware::from_fn_with_state,
                routing::{get, post},
            };
            use tower::ServiceExt;
            let app = Router::new()
                .route("/read", get(|| async { "must not read stale state" }))
                .route("/write", post(|| async { StatusCode::OK }))
                .layer(from_fn_with_state(
                    Some(short.clone()),
                    read_fence_middleware,
                ));
            assert_eq!(
                app.clone()
                    .oneshot(
                        HttpRequest::builder()
                            .uri("/read")
                            .body(Body::empty())
                            .unwrap()
                    )
                    .await
                    .unwrap()
                    .status(),
                StatusCode::SERVICE_UNAVAILABLE
            );
            assert_eq!(
                app.clone()
                    .oneshot(
                        HttpRequest::builder()
                            .method("POST")
                            .uri("/write")
                            .body(Body::empty())
                            .unwrap()
                    )
                    .await
                    .unwrap()
                    .status(),
                StatusCode::SERVICE_UNAVAILABLE
            );
            sqlx::query("SELECT pg_wal_replay_resume()")
                .execute(&replica)
                .await
                .unwrap();
            fence.wait().await.unwrap();
            short.wait().await.unwrap();
            assert_eq!(
                sqlx::query_scalar::<_, i32>(&format!("SELECT value FROM {table}"))
                    .fetch_one(&replica)
                    .await
                    .unwrap(),
                4
            );
        })
        .catch_unwind()
        .await;
        // Always resume the local test standby, including assertion failures.
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&replica)
            .await
            .unwrap();
        sqlx::query(&format!("DROP TABLE IF EXISTS {table}"))
            .execute(&primary)
            .await
            .unwrap();
        primary.close().await;
        replica.close().await;
        if let Err(panic) = result {
            std::panic::resume_unwind(panic);
        }
    }
}
