//! Batch completed mutations behind a separate synchronous commit.
//! Application transactions use durable local commits; this transaction owns
//! only disposable barrier rows while waiting for the configured replicas to apply WAL.
use std::sync::{Arc, Mutex};
use std::time::Duration;

use sqlx::PgPool;
use tokio::sync::{Semaphore, SemaphorePermit, oneshot};

// Immutable image bytes no longer enter the metadata replication stream.
// Keep a finite upload budget while allowing more independent-store work.
pub(crate) const VISIBILITY_BATCHES: usize = 8;
const WRITE_SLOTS: u32 = 64;
const BYTES_PER_SLOT: u64 = 256 * 1024;
const SMALL_BODY_BYTES: u64 = 64 * 1024;
pub(crate) const SMALL_WRITE_SLOTS: usize = 128;
pub(crate) const IMAGE_RESPONSE_SLOTS: usize = 64;

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
    small_mutations: Semaphore,
    control_mutations: Semaphore,
    image_responses: Semaphore,
}

#[derive(Clone)]
pub struct NativeWriteFence(Arc<Inner>);

impl NativeWriteFence {
    pub fn new(primary: PgPool, timeout: Duration) -> Self {
        Self(Arc::new(Inner {
            primary,
            timeout,
            pending: Mutex::default(),
            in_flight: Semaphore::new(VISIBILITY_BATCHES),
            mutations: Semaphore::new(WRITE_SLOTS as usize),
            small_mutations: Semaphore::new(SMALL_WRITE_SLOTS),
            control_mutations: Semaphore::new(4),
            image_responses: Semaphore::new(IMAGE_RESPONSE_SLOTS),
        }))
    }

    /// Bound image requests awaiting processing or global visibility separately
    /// from the bytes being processed. Keep this permit until the response.
    pub async fn admit_image_response(&self) -> SemaphorePermit<'_> {
        self.0.image_responses.acquire().await
            .expect("image response admission stays open")
    }

    /// Bound bytes during image processing and metadata writes until visibility.
    /// Image handlers release this permit after processing; their separate
    /// response permit still bounds the number waiting for replica replay.
    pub async fn admit(
        &self,
        body_bytes: Option<u64>,
        control: bool,
        image_upload: bool,
    ) -> SemaphorePermit<'_> {
        // Small account/admin mutations must not queue behind image uploads.
        // They retain their own bounded budget until the same replay barrier,
        // so bypassing the image queue never bypasses visibility guarantees.
        if !image_upload && body_bytes.is_some_and(|bytes| bytes <= SMALL_BODY_BYTES) {
            if control {
                return self
                    .0
                    .control_mutations
                    .acquire()
                    .await
                    .expect("control write admission stays open");
            }
            return self
                .0
                .small_mutations
                .acquire()
                .await
                .expect("small write admission stays open");
        }
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

#[cfg(test)]
mod admission_tests {
    use super::*;
    use sqlx::postgres::PgPoolOptions;

    #[tokio::test]
    async fn tiny_images_cannot_consume_account_or_control_admission() {
        let db = PgPoolOptions::new()
            .connect_lazy("postgres://localhost/unused")
            .unwrap();
        let fence = NativeWriteFence::new(db, Duration::from_secs(1));
        let mut images = Vec::new();
        for _ in 0..WRITE_SLOTS {
            images.push(fence.admit(Some(16), false, true).await);
        }
        assert!(
            tokio::time::timeout(
                Duration::from_millis(20),
                fence.admit(Some(16), false, true)
            )
            .await
            .is_err()
        );
        let account = tokio::time::timeout(
            Duration::from_millis(100),
            fence.admit(Some(16), false, false),
        )
        .await
        .unwrap();
        let control = tokio::time::timeout(
            Duration::from_millis(100),
            fence.admit(Some(16), true, false),
        )
        .await
        .unwrap();
        images.pop();
        let image = tokio::time::timeout(
            Duration::from_millis(100),
            fence.admit(Some(16), false, true),
        )
        .await
        .unwrap();
        drop((images, account, control, image));
    }
}
