//! Durable immutable image bytes in a separate PostgreSQL cluster.
//! Metadata publication is the only authority for public image visibility.
use crate::config::Config;
use sqlx::PgPool;
use sqlx::postgres::{PgConnectOptions, PgPoolOptions};

#[derive(Clone)]
pub struct ImageStore {
    pub db: PgPool,
    pub writes_enabled: bool,
}

impl ImageStore {
    pub async fn from_env(config: &Config) -> Option<Self> {
        let host = std::env::var("ISUREAL_IMAGE_STORE_HOST").unwrap_or_default();
        let writes = std::env::var("ISUREAL_IMAGE_STORE_WRITES").unwrap_or_else(|_| "0".into());
        assert!(
            matches!(writes.as_str(), "0" | "1"),
            "invalid image store write flag"
        );
        if host.is_empty() {
            assert_eq!(writes, "0", "image writes require an image store");
            return None;
        }
        let port = std::env::var("ISUREAL_IMAGE_STORE_PORT")
            .unwrap_or_else(|_| "5433".into())
            .parse::<u16>()
            .expect("valid image store port");
        let options = PgConnectOptions::new()
            .host(&host)
            .port(port)
            .username(&config.db_user)
            .password(&config.db_password)
            .database("isureal_assets");
        let db = PgPoolOptions::new()
            .max_connections(16)
            .connect_with(options)
            .await
            .expect("independent image database is available");
        let durable: String = sqlx::query_scalar("SHOW synchronous_commit")
            .fetch_one(&db)
            .await
            .unwrap();
        assert_eq!(
            durable, "on",
            "image publication requires durable image commits"
        );
        for setting in ["fsync", "full_page_writes"] {
            let value: String = sqlx::query_scalar(&format!("SHOW {setting}"))
                .fetch_one(&db)
                .await
                .unwrap();
            assert_eq!(value, "on", "image durability requires {setting}");
        }
        let standby: bool = sqlx::query_scalar("SELECT pg_is_in_recovery()")
            .fetch_one(&db)
            .await
            .unwrap();
        assert!(
            !standby,
            "image store must be the independent writable primary"
        );
        let exists: bool = sqlx::query_scalar("SELECT to_regclass('astra_image_data') IS NOT NULL")
            .fetch_one(&db)
            .await
            .unwrap();
        assert!(
            exists,
            "install image store schema before deploying readers"
        );
        Some(Self {
            db,
            writes_enabled: writes == "1",
        })
    }

    pub async fn put(&self, id: &str, data: &[u8]) -> Result<(), sqlx::Error> {
        sqlx::query("INSERT INTO astra_image_data (id,data) VALUES ($1,$2)")
            .bind(id)
            .bind(data)
            .execute(&self.db)
            .await?;
        Ok(())
    }

    pub async fn get(&self, id: &str) -> Result<Vec<u8>, sqlx::Error> {
        sqlx::query_scalar("SELECT data FROM astra_image_data WHERE id=$1")
            .bind(id)
            .fetch_one(&self.db)
            .await
    }
}
