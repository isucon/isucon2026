use std::sync::LazyLock;

use tokio::sync::{Semaphore, SemaphorePermit};

static GRAPH_QUERIES: LazyLock<Option<Semaphore>> = LazyLock::new(|| {
    let concurrency: usize = std::env::var("ISUREAL_GRAPH_QUERY_CONCURRENCY")
        .unwrap_or_else(|_| "0".into())
        .parse()
        .expect("graph query concurrency");
    assert!(concurrency <= 16, "invalid graph query concurrency");
    (concurrency != 0).then(|| Semaphore::new(concurrency))
});

/// Queue costly reads before taking a connection needed by short queries.
/// Callers release the permit immediately after SQL completes, including errors.
pub async fn graph_query() -> Option<SemaphorePermit<'static>> {
    match &*GRAPH_QUERIES {
        Some(semaphore) => Some(
            semaphore
                .acquire()
                .await
                .expect("graph admission stays open"),
        ),
        None => None,
    }
}
