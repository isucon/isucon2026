//! Execute feed statements on a dedicated read-only pool.
//! Authentication remains on the ingress application's normal authentication path.
use anyhow::{Result, ensure};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{str::FromStr, time::Duration};

#[derive(Clone)]
pub struct RecommendationReads {
    pub db: PgPool,
    pub all_feeds: bool,
}

impl RecommendationReads {
    pub async fn connect(
        config: &crate::config::Config,
        host: &str,
        replica: Option<&str>,
        connections: u32,
    ) -> Result<Self> {
        let options = PgConnectOptions::from_str(&config.database_dsn())?.host(host);
        Self::connect_options(options, replica, connections).await
    }

    async fn connect_options(
        options: PgConnectOptions,
        replica: Option<&str>,
        connections: u32,
    ) -> Result<Self> {
        ensure!(
            (4..=64).contains(&connections),
            "invalid recommendation pool capacity"
        );
        let options = options
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
        let role_matches = match replica {
            Some(replica) => recovery && slot == replica,
            None => !recovery,
        };
        if !role_matches {
            db.close().await;
            anyhow::bail!("recommendation reads require the configured primary or standby");
        }
        Ok(Self {
            db,
            all_feeds: false,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn dedicated_reads_validate_primary_and_replica_roles() {
        let Ok(primary) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
            return;
        };
        let options = PgConnectOptions::from_str(&primary).unwrap();
        let primary = RecommendationReads::connect_options(options.clone(), None, 4)
            .await
            .unwrap();
        let readonly: String = sqlx::query_scalar("SHOW default_transaction_read_only")
            .fetch_one(&primary.db)
            .await
            .unwrap();
        assert_eq!(readonly, "on");
        primary.db.close().await;
        assert!(
            RecommendationReads::connect_options(options, Some("astra_europe"), 4)
                .await
                .is_err()
        );
        if let Ok(replica) = std::env::var("ISUREAL_FENCE_REPLICA_DSN") {
            let options = PgConnectOptions::from_str(&replica).unwrap();
            let inspect = PgPool::connect(&replica).await.unwrap();
            let slot: String = sqlx::query_scalar("SHOW primary_slot_name")
                .fetch_one(&inspect)
                .await
                .unwrap();
            inspect.close().await;
            assert!(
                RecommendationReads::connect_options(options.clone(), None, 4)
                    .await
                    .is_err()
            );
            assert!(
                RecommendationReads::connect_options(options.clone(), Some("wrong_slot"), 4)
                    .await
                    .is_err()
            );
            let replica = RecommendationReads::connect_options(options, Some(&slot), 4)
                .await
                .unwrap();
            replica.db.close().await;
        }
    }
}
