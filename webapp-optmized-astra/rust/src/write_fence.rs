//! Wait for global write visibility after committing, outside the DB transaction.
use axum::body::HttpBody as _;
use axum::extract::{Request, State};
use axum::http::{Method, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use crate::api_error::new_api_error;
use crate::logger;
use crate::native_write_fence::NativeWriteFence;
use crate::read_fence::ReadFence;

#[derive(Clone)]
pub enum WriteFence {
    Polling(Vec<ReadFence>),
    Native(NativeWriteFence),
}

impl WriteFence {
    pub fn new(replicas: Vec<ReadFence>) -> Self {
        assert_eq!(replicas.len(), 2, "both regional replicas must be fenced");
        Self::Polling(replicas)
    }

    pub async fn wait(&self) -> Result<(), String> {
        match self {
            Self::Polling(replicas) => {
                futures_util::future::try_join_all(replicas.iter().map(ReadFence::wait)).await?;
            }
            Self::Native(fence) => fence.wait().await?,
        }
        Ok(())
    }
}

pub async fn write_fence_middleware(
    State(fence): State<Option<WriteFence>>,
    req: Request,
    next: Next,
) -> Response {
    let mutation = !matches!(*req.method(), Method::GET | Method::HEAD);
    let _admission = if mutation && let Some(WriteFence::Native(native)) = &fence {
        Some(
            native
                .admit(
                    req.body().size_hint().exact(),
                    req.uri().path() == "/api/admin/posting-window",
                    (req.method() == Method::POST
                        && crate::upload_proxy::is_upload_path(req.uri().path()))
                        || (req.method() == Method::PUT
                            && req.uri().path() == "/api/me/profile-image"),
                )
                .await,
        )
    } else {
        None
    };
    let response = next.run(req).await;
    // Capture WAL only after the handler has completed its commits. Failed
    // responses are fenced as well, since a handler may have committed work.
    // The outer run_to_completion layer also finishes this after disconnects.
    if mutation
        && let Some(fence) = &fence
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
    use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
    use std::panic::AssertUnwindSafe;
    use std::str::FromStr;
    use std::time::Duration;
    use tokio::time::{sleep, timeout};
    use tower::ServiceExt;

    fn app(primary: PgPool, table: String, fence: WriteFence) -> Router {
        let mutation = post(move || {
            let primary = primary.clone();
            let table = table.clone();
            async move {
                sqlx::query(&format!("UPDATE {table} SET value=value+1"))
                    .execute(&primary)
                    .await
                    .unwrap();
                StatusCode::NO_CONTENT
            }
        });
        Router::new()
            .route("/write", mutation.clone())
            .route("/api/admin/posting-window", mutation)
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
        check_released_locks(false).await;
    }

    #[tokio::test]
    async fn native_commit_waits_for_both_replicas_without_application_locks() {
        check_released_locks(true).await;
    }

    async fn check_released_locks(native: bool) {
        let (Ok(primary_dsn), Ok(replica_dsn), Ok(replica2_dsn)) = (
            std::env::var("ISUREAL_FENCE_PRIMARY_DSN"),
            std::env::var("ISUREAL_FENCE_REPLICA_DSN"),
            std::env::var("ISUREAL_FENCE_REPLICA2_DSN"),
        ) else {
            return;
        };
        let _serial = crate::read_fence::TEST_REPLICA_LOCK.lock().await;
        let primary = PgPoolOptions::new()
            .connect_with(
                PgConnectOptions::from_str(&primary_dsn)
                    .unwrap()
                    .options([("synchronous_commit", "local")]),
            )
            .await
            .unwrap();
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
        let fence = if native {
            WriteFence::Native(NativeWriteFence::new(
                primary.clone(),
                Duration::from_secs(3),
            ))
        } else {
            WriteFence::new(vec![
                ReadFence::new(primary.clone(), replica.clone(), Duration::from_secs(3)),
                ReadFence::new(primary.clone(), replica2.clone(), Duration::from_secs(3)),
            ])
        };
        let result = AssertUnwindSafe(async {
            if native {
                let previous: String = sqlx::query_scalar("SHOW synchronous_standby_names")
                    .fetch_one(&primary)
                    .await
                    .unwrap();
                assert_eq!(
                    previous, "",
                    "native test requires a dedicated asynchronous test primary"
                );
                sqlx::query(
                    "CREATE TABLE IF NOT EXISTS astra_visibility_barriers (id text PRIMARY KEY)",
                )
                .execute(&primary)
                .await
                .unwrap();
                sqlx::query("ALTER SYSTEM SET synchronous_standby_names = 'FIRST 2 (*)'")
                    .execute(&primary)
                    .await
                    .unwrap();
                sqlx::query("SELECT pg_reload_conf()")
                    .execute(&primary)
                    .await
                    .unwrap();
                timeout(Duration::from_secs(3), async {
                    while sqlx::query_scalar::<_, i64>(
                        "SELECT count(*) FROM pg_stat_replication WHERE sync_state='sync'",
                    )
                    .fetch_one(&primary)
                    .await
                    .unwrap()
                        != 2
                    {
                        sleep(Duration::from_millis(10)).await;
                    }
                })
                .await
                .unwrap();
            }
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
            if native {
                // The second batch must reach its own replay barrier while
                // the first is still pending; it cannot reuse an older LSN.
                let barriers: i64 = sqlx::query_scalar(
                    "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND query='COMMIT' AND wait_event='SyncRep'",
                ).fetch_one(&primary).await.unwrap();
                assert!(barriers >= 2, "only {barriers} concurrent barriers");
            }
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
            if native {
                // A timeout after the application commit must never return its
                // successful response while one standby is still behind.
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
                let short = WriteFence::Native(NativeWriteFence::new(
                    primary.clone(),
                    Duration::from_millis(100),
                ));
                let response = timeout(
                    Duration::from_secs(2),
                    super::tests::app(primary.clone(), table.clone(), short).oneshot(
                        HttpRequest::builder()
                            .method("POST")
                            .uri("/write")
                            .body(Body::empty())
                            .unwrap(),
                    ),
                )
                .await
                .unwrap()
                .unwrap();
                assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
                assert_eq!(value(&primary, &table).await, 4);
                assert_eq!(value(&replica2, &table).await, 3);
                sqlx::query("SELECT pg_wal_replay_resume()")
                    .execute(&replica2)
                    .await
                    .unwrap();
                fence.wait().await.unwrap();
                assert_eq!(value(&replica, &table).await, 4);
                assert_eq!(value(&replica2, &table).await, 4);
                assert_eq!(
                    sqlx::query_scalar::<_, i64>("SELECT count(*) FROM astra_visibility_barriers")
                        .fetch_one(&primary)
                        .await
                        .unwrap(),
                    0
                );
                // A large body consumes the admission budget until remote
                // visibility, even after its handler releases all SQL locks.
                sqlx::query("SELECT pg_wal_replay_pause()")
                    .execute(&replica2).await.unwrap();
                timeout(Duration::from_secs(2), async {
                    while !sqlx::query_scalar::<_, bool>("SELECT pg_get_wal_replay_pause_state()='paused'")
                        .fetch_one(&replica2).await.unwrap() {
                        sleep(Duration::from_millis(5)).await;
                    }
                }).await.unwrap();
                let large = tokio::spawn(app.clone().oneshot(
                    HttpRequest::builder().method("POST").uri("/write")
                        .body(Body::from(vec![0u8; 16 * 1024 * 1024])).unwrap(),
                ));
                wait_value(&primary, &table, 5).await;
                let mut queued = tokio::spawn(app.clone().oneshot(
                    HttpRequest::builder().method("POST").uri("/write")
                        .body(Body::from(vec![0u8; 16 * 1024 * 1024])).unwrap(),
                ));
                assert!(timeout(Duration::from_millis(100), &mut queued).await.is_err());
                assert_eq!(value(&primary, &table).await, 5, "queued mutation bypassed admission");
                assert_eq!(value(&replica2, &table).await, 4);
                // An account/admin-sized write bypasses the upload admission
                // queue, but its response still waits for both replicas.
                let mut small = tokio::spawn(app.clone().oneshot(
                    HttpRequest::builder().method("POST").uri("/write")
                        .body(Body::empty()).unwrap(),
                ));
                wait_value(&primary, &table, 6).await;
                assert!(timeout(Duration::from_millis(100), &mut small).await.is_err());
                assert_eq!(value(&replica2, &table).await, 4);
                assert_eq!(timeout(Duration::from_millis(100), app.clone().oneshot(
                    HttpRequest::get("/read").body(Body::empty()).unwrap(),
                )).await.unwrap().unwrap().status(), StatusCode::OK);
                sqlx::query("SELECT pg_wal_replay_resume()")
                    .execute(&replica2).await.unwrap();
                for response in [large, queued, small] {
                    assert_eq!(timeout(Duration::from_secs(3), response).await.unwrap().unwrap().unwrap().status(), StatusCode::NO_CONTENT);
                }
                assert_eq!(value(&replica, &table).await, 7);
                assert_eq!(value(&replica2, &table).await, 7);
                // Saturating normal metadata writes must not prevent opening
                // the next posting window. Its response still waits for replay.
                sqlx::query("SELECT pg_wal_replay_pause()").execute(&replica2).await.unwrap();
                timeout(Duration::from_secs(2), async {
                    while !sqlx::query_scalar::<_, bool>("SELECT pg_get_wal_replay_pause_state()='paused'")
                        .fetch_one(&replica2).await.unwrap() {
                        sleep(Duration::from_millis(5)).await;
                    }
                }).await.unwrap();
                let mut ordinary = Vec::new();
                let admitted = crate::native_write_fence::SMALL_WRITE_SLOTS as i32;
                for _ in 0..admitted {
                    ordinary.push(tokio::spawn(app.clone().oneshot(
                        HttpRequest::post("/write").body(Body::empty()).unwrap(),
                    )));
                }
                wait_value(&primary, &table, 7 + admitted).await;
                let mut queued_small = tokio::spawn(app.clone().oneshot(
                    HttpRequest::post("/write").body(Body::empty()).unwrap(),
                ));
                assert!(timeout(Duration::from_millis(100), &mut queued_small).await.is_err());
                assert_eq!(value(&primary, &table).await, 7 + admitted);
                let mut control = tokio::spawn(app.clone().oneshot(
                    HttpRequest::post("/api/admin/posting-window").body(Body::empty()).unwrap(),
                ));
                wait_value(&primary, &table, 8 + admitted).await;
                assert!(timeout(Duration::from_millis(100), &mut control).await.is_err());
                assert_eq!(value(&replica2, &table).await, 7);
                sqlx::query("SELECT pg_wal_replay_resume()").execute(&replica2).await.unwrap();
                ordinary.extend([queued_small, control]);
                for response in ordinary {
                    assert_eq!(timeout(Duration::from_secs(3), response).await.unwrap().unwrap().unwrap().status(), StatusCode::NO_CONTENT);
                }
                assert_eq!(value(&replica, &table).await, 9 + admitted);
                assert_eq!(value(&replica2, &table).await, 9 + admitted);

            }
        })
        .catch_unwind()
        .await;
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&replica2)
            .await
            .unwrap();
        if native {
            sqlx::query("ALTER SYSTEM SET synchronous_standby_names = ''")
                .execute(&primary)
                .await
                .unwrap();
            sqlx::query("SELECT pg_reload_conf()")
                .execute(&primary)
                .await
                .unwrap();
            sqlx::query("DROP TABLE IF EXISTS astra_visibility_barriers")
                .execute(&primary)
                .await
                .unwrap();
        }
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
