//! Batch completed mutations behind a separate synchronous commit.
//! Application transactions use durable local commits; this transaction owns
//! only disposable barrier rows while waiting for both replicas to apply WAL.
use std::sync::{Arc, Mutex};
use std::time::Duration;

use sqlx::PgPool;
use tokio::sync::{Semaphore, SemaphorePermit, oneshot};

const WRITE_SLOTS: u32 = 32;
const BYTES_PER_SLOT: u64 = 256 * 1024;

#[derive(Default)]
struct Pending {
    running: bool,
    waiters: Vec<oneshot::Sender<Result<(), String>>>,
}

struct Inner {
    primary: PgPool,
    timeout: Duration,
    pending: Mutex<Pending>,
    in_flight: Semaphore,
    mutations: Semaphore,
}

#[derive(Clone)]
pub struct NativeWriteFence(Arc<Inner>);

impl NativeWriteFence {
    pub fn new(primary: PgPool, timeout: Duration) -> Self {
        Self(Arc::new(Inner {
            primary,
            timeout,
            pending: Mutex::default(),
            in_flight: Semaphore::new(4),
            mutations: Semaphore::new(WRITE_SLOTS as usize),
        }))
    }

    /// Keep admission until the response barrier has applied on both replicas.
    /// Local commits release SQL locks immediately, but cannot build an
    /// unbounded queue of large image writes ahead of regional replay.
    pub async fn admit(&self, body_bytes: Option<u64>) -> SemaphorePermit<'_> {
        let slots = body_bytes
            .map(|bytes| {
                bytes
                    .div_ceil(BYTES_PER_SLOT)
                    .clamp(1, u64::from(WRITE_SLOTS)) as u32
            })
            .unwrap_or(WRITE_SLOTS);
        self.0
            .mutations
            .acquire_many(slots)
            .await
            .expect("write admission stays open")
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
        tokio::time::timeout(self.0.timeout, receiver)
            .await
            .map_err(|_| "native replica catch-up timed out".to_owned())?
            .map_err(|_| "native write fence worker stopped".to_owned())?
    }

    async fn run(self) {
        // A bounded number of batches can wait for replay concurrently. Keep a
        // single collector while all slots are busy, so queued mutations merge
        // into its next batch rather than spawning one transaction per request.
        let _permit = self.0.in_flight.acquire().await.expect("fence stays open");
        tokio::time::sleep(Duration::from_millis(1)).await;
        let waiters = {
            let mut pending = self.0.pending.lock().unwrap();
            pending.waiters.retain(|waiter| !waiter.is_closed());
            pending.running = false;
            std::mem::take(&mut pending.waiters)
        };
        if waiters.is_empty() {
            return;
        }
        // Only mutations completed before this transaction are covered.
        // Later arrivals get a distinct, later WAL commit, even while this
        // batch's acknowledgement is still pending.
        let result = tokio::time::timeout(self.0.timeout, self.synchronize())
            .await
            .map_err(|_| "native replica catch-up timed out".to_owned())
            .and_then(|result| result.map_err(|err| format!("native write barrier: {err}")));
        for waiter in waiters {
            let _ = waiter.send(result.clone());
        }
    }

    async fn synchronize(&self) -> Result<(), sqlx::Error> {
        let mut tx = self.0.primary.begin().await?;
        // A server statement timeout can cancel SyncRep without rolling back
        // a committed transaction. Use the client deadline, which fails closed.
        sqlx::query("SET LOCAL synchronous_commit = remote_apply")
            .execute(&mut *tx)
            .await?;
        sqlx::query("SET LOCAL statement_timeout = 0")
            .execute(&mut *tx)
            .await?;
        let id = uuid::Uuid::new_v4().to_string();
        sqlx::query("INSERT INTO astra_visibility_barriers (id) VALUES ($1)")
            .bind(&id)
            .execute(&mut *tx)
            .await?;
        sqlx::query("DELETE FROM astra_visibility_barriers WHERE id = $1")
            .bind(&id)
            .execute(&mut *tx)
            .await?;
        // This WAL commit record follows every covered application commit.
        // Replicas replay in order, so its acknowledgement covers those writes.
        tx.commit().await
    }
}
