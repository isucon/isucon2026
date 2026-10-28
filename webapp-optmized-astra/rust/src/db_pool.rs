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
