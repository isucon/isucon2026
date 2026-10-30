//! Reuse recently successful connections without a redundant health-check RTT.
use sqlx::{Connection, postgres::PgPoolOptions};
use std::time::Duration;

pub fn options(max_connections: u32) -> PgPoolOptions {
    PgPoolOptions::new()
        .max_connections(max_connections)
        .test_before_acquire(false)
        .before_acquire(|connection, metadata| {
            Box::pin(async move {
                // Every caller still executes its fresh SQL. Idle connections
                // are checked before reuse; query failures are never retried.
                if metadata.idle_for >= Duration::from_secs(1) {
                    connection.ping().await?;
                }
                Ok(true)
            })
        })
}

/// Bound ordinary SQL concurrency separately from response-visibility barriers.
pub fn metadata_connections() -> u32 {
    let connections = std::env::var("ISUREAL_METADATA_POOL_CONNECTIONS")
        .unwrap_or_else(|_| "30".into())
        .parse()
        .expect("metadata pool capacity");
    assert!(
        (4..=64).contains(&connections),
        "metadata pool must have 4–64 connections"
    );
    connections
}

/// Keep short memory reads out of the feed/search connection queue. Clone the
/// same connection options, including the selected replica and search path.
pub async fn memory_context(
    context: &std::sync::Arc<crate::server::HandlerContext>,
    connections: u32,
) -> Result<std::sync::Arc<crate::server::HandlerContext>, sqlx::Error> {
    isolated_context(context, connections, "astra_memory").await
}

pub async fn short_read_context(
    context: &std::sync::Arc<crate::server::HandlerContext>,
    connections: u32,
) -> Result<std::sync::Arc<crate::server::HandlerContext>, sqlx::Error> {
    isolated_context(context, connections, "astra_short_reads").await
}

async fn isolated_context(
    context: &std::sync::Arc<crate::server::HandlerContext>,
    connections: u32,
    name: &str,
) -> Result<std::sync::Arc<crate::server::HandlerContext>, sqlx::Error> {
    assert!(
        connections <= 16,
        "isolated pool must have 0–16 connections"
    );
    if connections == 0 {
        return Ok(context.clone());
    }
    let db = options(connections)
        .connect_with(
            (*context.db.connect_options())
                .clone()
                .application_name(name),
        )
        .await?;
    Ok(std::sync::Arc::new(crate::server::HandlerContext {
        db,
        config: context.config.clone(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: context.notification_batch_writes,
        read_fence: context.read_fence.clone(),
        image_store: context.image_store.clone(),
    }))
}
