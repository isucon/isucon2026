//! Execute only the recommended-feed statement on an existing regional replica.
//! Authentication remains on the ingress application's normal authentication path.
use anyhow::{Result, ensure};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{str::FromStr, time::Duration};

#[derive(Clone)]
pub struct RecommendationReads(pub PgPool);

impl RecommendationReads {
    pub async fn connect(
        config: &crate::config::Config,
        host: &str,
        replica: &str,
        connections: u32,
    ) -> Result<Self> {
        ensure!(
            (4..=64).contains(&connections),
            "invalid recommendation pool capacity"
        );
        let options = PgConnectOptions::from_str(&config.database_dsn())?
            .host(host)
            .statement_cache_capacity(512)
            .application_name("astra_recommendations")
            .options([("default_transaction_read_only", "on")]);
        // Establish the network connections before accepting requests. Fresh
        // statements detect broken connections without an extra health-check RTT.
        let db = PgPoolOptions::new()
            .max_connections(connections)
            .min_connections(connections)
            .test_before_acquire(false)
            .acquire_timeout(Duration::from_secs(15))
            .connect_with(options)
            .await?;
        let (recovery, slot): (bool, String) =
            sqlx::query_as("SELECT pg_is_in_recovery(),current_setting('primary_slot_name')")
                .fetch_one(&db)
                .await?;
        ensure!(
            recovery && slot == replica,
            "recommendation reads require the configured regional standby"
        );
        Ok(Self(db))
    }
}
