use std::collections::HashMap;
use std::convert::Infallible;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use axum::Extension;
use axum::body::Body;
use axum::extract::State;
use axum::http::{StatusCode, header};
use axum::response::Response;
use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use serde::Serialize;
use sqlx::PgPool;
use tokio::sync::oneshot;
use tokio::time::{MissedTickBehavior, interval};

use crate::api_error::Error;
use crate::image_signature::image_signing_key;
use crate::logger;
use crate::read_fence::ReadFence;
use crate::server::HandlerContext;
use crate::session::Session;
use crate::timefmt::rfc3339_nano;

#[derive(Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct GetNotificationsResponse {
    pub id: String,
    pub posting_window_id: String,
    pub region: String,
    #[serde(serialize_with = "rfc3339_nano")]
    pub created_at: DateTime<Utc>,
}

#[derive(Clone)]
struct Recipient {
    id: u64,
    user_id: String,
    region: String,
    connected_at: DateTime<Utc>,
}

struct Subscription {
    recipient: Recipient,
    sender: oneshot::Sender<String>,
}

#[derive(Default)]
struct Subscribers {
    next_id: u64,
    running: bool,
    entries: HashMap<u64, Subscription>,
}

/// Each database pool has one poller. It batches pending SSE subscribers while
/// checking their current account state and each connection's timestamp.
#[derive(Clone, Default)]
pub struct NotificationHub(Arc<Mutex<Subscribers>>);

#[derive(sqlx::FromRow)]
struct NotificationRow {
    slot: i64,
    secret_key_hash: Option<String>,
    id: Option<String>,
    posting_window_id: Option<String>,
    region: Option<String>,
    created_at: Option<DateTime<Utc>>,
}

impl NotificationHub {
    fn subscribe(
        &self,
        db: PgPool,
        fence: Option<ReadFence>,
        user_id: String,
        region: String,
        connected_at: DateTime<Utc>,
    ) -> oneshot::Receiver<String> {
        let (sender, receiver) = oneshot::channel();
        let mut state = self.0.lock().unwrap();
        let id = state.next_id;
        state.next_id += 1;
        state.entries.insert(
            id,
            Subscription {
                recipient: Recipient {
                    id,
                    user_id,
                    region,
                    connected_at,
                },
                sender,
            },
        );
        if !state.running {
            state.running = true;
            let hub = self.clone();
            tokio::spawn(async move { hub.poll(db, fence).await });
        }
        receiver
    }

    async fn poll(self, db: PgPool, fence: Option<ReadFence>) {
        let mut ticker = interval(Duration::from_millis(100));
        ticker.set_missed_tick_behavior(MissedTickBehavior::Skip);
        loop {
            ticker.tick().await;
            let recipients: Vec<Recipient> = {
                let mut state = self.0.lock().unwrap();
                state.entries.retain(|_, entry| !entry.sender.is_closed());
                if state.entries.is_empty() {
                    state.running = false;
                    return;
                }
                state
                    .entries
                    .values()
                    .map(|entry| entry.recipient.clone())
                    .collect()
            };
            if let Some(fence) = &fence
                && let Err(err) = fence.wait().await
            {
                logger::error("failed to synchronize notification replica", &err);
                let mut state = self.0.lock().unwrap();
                for recipient in &recipients {
                    state.entries.remove(&recipient.id);
                }
                continue;
            }
            // Chunking bounds the query and lets other database work interleave
            // when thousands of clients are waiting for a notification.
            for batch in recipients.chunks(512) {
                let rows = sqlx::query_as::<_, NotificationRow>(
                    r#"
                    SELECT s.slot, u.secret_key_hash,
                           n.id, n.posting_window_id, n.region, n.created_at
                    FROM unnest($1::text[], $2::text[], $3::timestamptz[])
                         WITH ORDINALITY AS s(user_id, region, connected_at, slot)
                    LEFT JOIN users u ON u.id = s.user_id
                         AND u.status = 'active' AND u.region = s.region
                    LEFT JOIN LATERAL (
                        SELECT n.id, n.posting_window_id, w.region, n.created_at
                        FROM notifications n
                        JOIN posting_windows w ON w.id = n.posting_window_id
                        WHERE n.user_id = u.id AND n.created_at > s.connected_at
                          AND w.ended_at IS NULL
                        ORDER BY n.created_at, n.id LIMIT 1
                    ) n ON true
                "#,
                )
                .bind(batch.iter().map(|r| r.user_id.as_str()).collect::<Vec<_>>())
                .bind(batch.iter().map(|r| r.region.as_str()).collect::<Vec<_>>())
                .bind(batch.iter().map(|r| r.connected_at).collect::<Vec<_>>())
                .fetch_all(&db)
                .await;
                match rows {
                    Ok(rows) => {
                        for row in rows {
                            let recipient = &batch[row.slot as usize - 1];
                            let valid = row
                                .secret_key_hash
                                .as_deref()
                                .is_some_and(|key| image_signing_key(key).is_ok());
                            if !valid {
                                self.0.lock().unwrap().entries.remove(&recipient.id);
                                continue;
                            }
                            if let (
                                Some(id),
                                Some(posting_window_id),
                                Some(region),
                                Some(created_at),
                            ) = (row.id, row.posting_window_id, row.region, row.created_at)
                            {
                                let notification = GetNotificationsResponse {
                                    id,
                                    posting_window_id,
                                    region,
                                    created_at,
                                };
                                let data = serde_json::to_string(&notification);
                                let entry = self.0.lock().unwrap().entries.remove(&recipient.id);
                                if let Some(entry) = entry {
                                    match data {
                                        Ok(data) => {
                                            let _ = entry.sender.send(format!("data: {data}\n\n"));
                                        }
                                        Err(err) => {
                                            logger::error("failed to encode notification", &err)
                                        }
                                    }
                                }
                            }
                        }
                    }
                    Err(err) => {
                        logger::error("failed to get notification batch", &err);
                        let mut state = self.0.lock().unwrap();
                        for recipient in batch {
                            state.entries.remove(&recipient.id);
                        }
                    }
                }
            }
        }
    }
}

pub async fn get_notifications_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
) -> Result<Response, Error> {
    let connected_at: DateTime<Utc> = sqlx::query_scalar("SELECT clock_timestamp()")
        .fetch_one(&h.db)
        .await?;
    let receiver = h.notifications.subscribe(
        h.db.clone(),
        h.read_fence.clone(),
        session.user_id,
        session.region,
        connected_at,
    );
    let event = async move { receiver.await.ok().map(Ok::<_, Infallible>) };
    let stream = futures_util::stream::once(event).filter_map(|event| async move { event });
    Ok(Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "text/event-stream")
        .body(Body::from_stream(stream))
        .map_err(anyhow::Error::from)?)
}

#[cfg(test)]
mod tests {
    use super::*;
    use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
    use std::str::FromStr;
    use tokio::time::{sleep, timeout};

    #[tokio::test]
    async fn batched_notifications_preserve_connection_and_recipient_rules() {
        let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
            return;
        };
        let schema = format!("astra_notifications_{}", uuid::Uuid::new_v4().simple());
        let admin = PgPool::connect(&dsn).await.unwrap();
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .unwrap();
        let options = PgConnectOptions::from_str(&dsn)
            .unwrap()
            .options([("search_path", schema.as_str())]);
        let db = PgPoolOptions::new()
            .max_connections(3)
            .connect_with(options)
            .await
            .unwrap();
        sqlx::raw_sql(r#"
            CREATE TABLE users (id text PRIMARY KEY, region text, status text, secret_key_hash text);
            CREATE TABLE posting_windows (id text PRIMARY KEY, region text, ended_at timestamptz);
            CREATE TABLE notifications (id text PRIMARY KEY, user_id text, posting_window_id text, created_at timestamptz);
            INSERT INTO users SELECT id, 'asia', 'active', repeat('ab',32) FROM unnest(ARRAY['a','b','c','d','bad']) id;
            UPDATE users SET secret_key_hash = 'invalid' WHERE id = 'bad';
            INSERT INTO posting_windows VALUES ('active', 'asia', NULL), ('ended', 'asia', now());
        "#).execute(&db).await.unwrap();
        let connected: DateTime<Utc> = sqlx::query_scalar("SELECT clock_timestamp()")
            .fetch_one(&db)
            .await
            .unwrap();
        sqlx::query("INSERT INTO notifications VALUES ('history','a','active',$1), ('ended','a','ended',$2)")
            .bind(connected - chrono::Duration::seconds(1))
            .bind(connected + chrono::Duration::seconds(1))
            .execute(&db).await.unwrap();
        let hub = NotificationHub::default();
        let subscribe =
            |user: &str, when| hub.subscribe(db.clone(), None, user.into(), "asia".into(), when);
        let mut first = subscribe("a", connected);
        let mut later = subscribe("a", connected + chrono::Duration::seconds(2));
        let other = subscribe("b", connected);
        let moved = subscribe("c", connected);
        let deleted = subscribe("d", connected);
        let invalid = subscribe("bad", connected);
        let cancelled = subscribe("b", connected);
        drop(cancelled);
        assert!(
            timeout(Duration::from_millis(220), &mut first)
                .await
                .is_err()
        );
        assert!(
            timeout(Duration::from_secs(2), invalid)
                .await
                .unwrap()
                .is_err()
        );
        sqlx::query("INSERT INTO notifications VALUES ('a_second','a','active',$1), ('a_first','a','active',$1), ('b_first','b','active',$1)")
            .bind(connected + chrono::Duration::seconds(1)).execute(&db).await.unwrap();
        let event = timeout(Duration::from_secs(2), first)
            .await
            .unwrap()
            .unwrap();
        assert!(event.starts_with("data: "));
        assert!(event.ends_with("\n\n"));
        let payload: serde_json::Value =
            serde_json::from_str(event.trim_start_matches("data: ").trim()).unwrap();
        assert_eq!(payload["id"], "a_first");
        assert_eq!(payload["postingWindowId"], "active");
        assert_eq!(payload["region"], "asia");
        assert!(
            timeout(Duration::from_secs(2), other)
                .await
                .unwrap()
                .unwrap()
                .contains("b_first")
        );
        assert!(
            timeout(Duration::from_millis(220), &mut later)
                .await
                .is_err()
        );
        sqlx::raw_sql(
            "UPDATE users SET region = 'europe' WHERE id = 'c'; DELETE FROM users WHERE id = 'd';",
        )
        .execute(&db)
        .await
        .unwrap();
        assert!(
            timeout(Duration::from_secs(2), moved)
                .await
                .unwrap()
                .is_err()
        );
        assert!(
            timeout(Duration::from_secs(2), deleted)
                .await
                .unwrap()
                .is_err()
        );
        sqlx::query("INSERT INTO notifications VALUES ('a_new','a','active',$1)")
            .bind(connected + chrono::Duration::seconds(3))
            .execute(&db)
            .await
            .unwrap();
        assert!(
            timeout(Duration::from_secs(2), later)
                .await
                .unwrap()
                .unwrap()
                .contains("a_new")
        );
        sleep(Duration::from_millis(150)).await;
        {
            let state = hub.0.lock().unwrap();
            assert!(state.entries.is_empty());
            assert!(!state.running);
        }
        // A stopped hub must restart without losing its first subscriber.
        let restarted = subscribe("a", connected + chrono::Duration::seconds(2));
        assert!(
            timeout(Duration::from_secs(2), restarted)
                .await
                .unwrap()
                .unwrap()
                .contains("a_new")
        );
        sleep(Duration::from_millis(150)).await;
        db.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await
            .unwrap();
        admin.close().await;
    }

    #[tokio::test]
    async fn fenced_poll_does_not_deliver_to_a_deleted_recipient_on_lagging_replica() {
        use futures_util::FutureExt;
        use std::panic::AssertUnwindSafe;
        let (Ok(primary_dsn), Ok(replica_dsn)) = (
            std::env::var("ISUREAL_FENCE_PRIMARY_DSN"),
            std::env::var("ISUREAL_FENCE_REPLICA_DSN"),
        ) else {
            return;
        };
        let _serial = crate::read_fence::TEST_REPLICA_LOCK.lock().await;
        let admin = PgPool::connect(&primary_dsn).await.unwrap();
        let replica_admin = PgPool::connect(&replica_dsn).await.unwrap();
        let schema = format!("astra_notify_fence_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .unwrap();
        let db = PgPoolOptions::new()
            .max_connections(3)
            .connect_with(
                PgConnectOptions::from_str(&primary_dsn)
                    .unwrap()
                    .options([("search_path", schema.as_str())]),
            )
            .await
            .unwrap();
        let replica = PgPoolOptions::new()
            .max_connections(3)
            .connect_with(
                PgConnectOptions::from_str(&replica_dsn)
                    .unwrap()
                    .options([("search_path", schema.as_str())]),
            )
            .await
            .unwrap();
        let fence = ReadFence::new(admin.clone(), replica_admin.clone(), Duration::from_secs(3));
        let result = AssertUnwindSafe(async {
            sqlx::raw_sql(r#"
                CREATE TABLE users (id text PRIMARY KEY, region text, status text, secret_key_hash text);
                CREATE TABLE posting_windows (id text PRIMARY KEY, region text, ended_at timestamptz);
                CREATE TABLE notifications (id text PRIMARY KEY, user_id text, posting_window_id text, created_at timestamptz);
                INSERT INTO users VALUES ('recipient','asia','active',repeat('ab',32));
                INSERT INTO posting_windows VALUES ('window','asia',NULL);
                INSERT INTO notifications VALUES ('notice','recipient','window',clock_timestamp());
            "#).execute(&db).await.unwrap();
            let connected: DateTime<Utc> = sqlx::query_scalar("SELECT created_at - interval '1 second' FROM notifications")
                .fetch_one(&db).await.unwrap();
            fence.wait().await.unwrap();
            sqlx::query("SELECT pg_wal_replay_pause()").execute(&replica_admin).await.unwrap();
            timeout(Duration::from_secs(2), async {
                loop {
                    if sqlx::query_scalar::<_,bool>("SELECT pg_get_wal_replay_pause_state()='paused'").fetch_one(&replica_admin).await.unwrap() { break; }
                    sleep(Duration::from_millis(5)).await;
                }
            }).await.unwrap();
            sqlx::query("UPDATE users SET status='deleted'").execute(&db).await.unwrap();
            let hub = NotificationHub::default();
            let mut pending = hub.subscribe(replica.clone(), Some(fence.clone()), "recipient".into(), "asia".into(), connected);
            assert!(timeout(Duration::from_millis(120), &mut pending).await.is_err());
            sqlx::query("SELECT pg_wal_replay_resume()").execute(&replica_admin).await.unwrap();
            assert!(timeout(Duration::from_secs(3), pending).await.unwrap().is_err());
            sqlx::query("UPDATE users SET status='active'").execute(&db).await.unwrap();
            let pending = hub.subscribe(replica.clone(), Some(fence.clone()), "recipient".into(), "asia".into(), connected);
            assert!(timeout(Duration::from_secs(3), pending).await.unwrap().unwrap().contains("notice"));
            sleep(Duration::from_millis(150)).await;
        }).catch_unwind().await;
        sqlx::query("SELECT pg_wal_replay_resume()")
            .execute(&replica_admin)
            .await
            .unwrap();
        db.close().await;
        replica.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await
            .unwrap();
        admin.close().await;
        replica_admin.close().await;
        if let Err(panic) = result {
            std::panic::resume_unwind(panic);
        }
    }
}
