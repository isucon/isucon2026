//! Durable immutable image bytes in a separate PostgreSQL cluster.
//! Metadata publication is the only authority for public image visibility.
use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, Mutex};

use crate::config::Config;
use bytes::Bytes;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use sqlx::postgres::PgConnectOptions;

#[derive(Clone)]
pub struct ImageStore {
    pub db: PgPool,
    pub writes_enabled: bool,
    pub cache_peers: Option<crate::image_cache_push::CachePeers>,
    cache: Option<Arc<Mutex<AssetCache>>>,
    regional: HashMap<String, PgPool>,
    files: Option<crate::file_store::FileStore>,
}

impl ImageStore {
    pub fn new(
        db: PgPool,
        writes_enabled: bool,
        cache_peers: Option<crate::image_cache_push::CachePeers>,
    ) -> Self {
        Self {
            db,
            writes_enabled,
            cache_peers,
            cache: Some(Arc::new(Mutex::new(AssetCache::default()))),
            regional: HashMap::new(),
            files: None,
        }
    }

    pub fn with_regional_pools(mut self, pools: HashMap<String, PgPool>) -> Self {
        assert!(
            pools
                .keys()
                .all(|region| matches!(region.as_str(), "europe" | "america"))
        );
        self.regional = pools;
        self
    }

    pub fn with_file_store(mut self, files: Option<crate::file_store::FileStore>) -> Self {
        self.files = files;
        self
    }

    pub fn has_region(&self, region: &str) -> bool {
        self.regional.contains_key(region)
    }

    pub async fn put_regional(
        &self,
        region: &str,
        id: &str,
        data: &Bytes,
    ) -> Result<String, sqlx::Error> {
        if let Some(files) = &self.files
            && files.writes_enabled
        {
            if !files.is_local(region) {
                return Err(sqlx::Error::Protocol(
                    "file writes must stay in the receiving region".into(),
                ));
            }
            return files.put(id, data).await;
        }
        let db = self.regional.get(region).ok_or(sqlx::Error::RowNotFound)?;
        sqlx::query("INSERT INTO astra_image_data (id,data) VALUES ($1,$2)")
            .bind(id)
            .bind(&data[..])
            .execute(db)
            .await?;
        Ok(format!("{region}:{id}"))
    }

    pub fn with_cache_enabled(mut self, enabled: bool) -> Self {
        if !enabled {
            self.cache = None;
        }
        self
    }

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
        let db = crate::db_pool::options(16)
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
        let cache = std::env::var("ISUREAL_IMAGE_STORE_CACHE").unwrap_or_else(|_| "0".into());
        assert!(
            matches!(cache.as_str(), "0" | "1"),
            "invalid asset cache flag"
        );
        let mapping: HashMap<String, String> = serde_json::from_str(
            &std::env::var("ISUREAL_REGIONAL_IMAGE_HOSTS_JSON").unwrap_or_else(|_| "{}".into()),
        )
        .expect("valid regional image DB mapping");
        let mut regional = HashMap::new();
        for (region, host) in mapping {
            assert!(matches!(
                (region.as_str(), host.as_str()),
                ("europe", "192.168.146.10") | ("america", "192.168.147.10")
            ));
            let options = PgConnectOptions::new()
                .host(&host)
                .port(5433)
                .username(&config.db_user)
                .password(&config.db_password)
                .database("isureal_assets");
            let pool = crate::db_pool::options(16)
                .connect_with(options)
                .await
                .expect("regional immutable image database is available");
            let safe: bool = sqlx::query_scalar("SELECT NOT pg_is_in_recovery() AND current_setting('synchronous_commit')='on' AND current_setting('fsync')='on' AND current_setting('full_page_writes')='on' AND to_regclass('astra_image_data') IS NOT NULL")
                .fetch_one(&pool).await.expect("regional image durability check");
            assert!(safe, "regional images require durable primary storage");
            regional.insert(region, pool);
        }
        Some(
            Self::new(
                db,
                writes == "1",
                crate::image_cache_push::CachePeers::from_env(),
            )
            .with_cache_enabled(cache == "1")
            .with_regional_pools(regional)
            .with_file_store(crate::file_store::FileStore::from_env(&config.region).await),
        )
    }

    pub async fn put(&self, id: &str, data: &Bytes) -> Result<String, sqlx::Error> {
        if let Some(files) = &self.files
            && files.writes_enabled
        {
            if !files.is_local("asia") {
                return Err(sqlx::Error::Protocol(
                    "unqualified file writes require the Asia writer".into(),
                ));
            }
            return files.put(id, data).await;
        }
        let hash: Option<[u8; 32]> = self.cache.as_ref().map(|_| Sha256::digest(data).into());
        if let (Some(cache), Some(hash)) = (&self.cache, &hash)
            && let Some(key) = cache.lock().unwrap().matching_key(hash, data)
        {
            return Ok(key);
        }
        sqlx::query("INSERT INTO astra_image_data (id,data) VALUES ($1,$2)")
            .bind(id)
            .bind(&data[..])
            .execute(&self.db)
            .await?;
        // Cache only after the durable INSERT completes. Concurrent misses may
        // store identical bytes twice, which is safe and needs no global lock.
        if let (Some(cache), Some(hash)) = (&self.cache, hash) {
            cache
                .lock()
                .unwrap()
                .insert(id.to_owned(), hash, data.clone());
        }
        Ok(id.to_owned())
    }

    pub async fn get(&self, id: &str) -> Result<Vec<u8>, sqlx::Error> {
        if id.starts_with("fs:") {
            let files = self
                .files
                .as_ref()
                .ok_or_else(|| sqlx::Error::Protocol("file image readers are required".into()))?;
            return files.get(id).await;
        }
        let cached = self
            .cache
            .as_ref()
            .and_then(|cache| cache.lock().unwrap().by_key.get(id).cloned());
        if let Some(data) = cached {
            return Ok(data.to_vec());
        }
        let (db, stored_id) = if let Some((region, stored_id)) = id.split_once(':') {
            let db = self.regional.get(region).ok_or(sqlx::Error::RowNotFound)?;
            (db, stored_id)
        } else {
            (&self.db, id)
        };
        let data: Vec<u8> = sqlx::query_scalar("SELECT data FROM astra_image_data WHERE id=$1")
            .bind(stored_id)
            .fetch_one(db)
            .await?;
        if let Some(cache) = &self.cache {
            let data = Bytes::from(data);
            let hash = Sha256::digest(&data).into();
            cache
                .lock()
                .unwrap()
                .insert(id.to_owned(), hash, data.clone());
            Ok(data.to_vec())
        } else {
            Ok(data)
        }
    }
}

// Asset rows are immutable (the app role has SELECT/INSERT only), and they are
// retained across initialize. A UUID remains an opaque storage reference; every
// public read still checks the current image metadata and owner credentials.
// The cache belongs to one database pool and is bounded independently from the
// public image cache. Offline orphan collection must restart all app processes.
struct AssetCache {
    by_key: HashMap<String, Bytes>,
    by_hash: HashMap<[u8; 32], String>,
    order: VecDeque<(String, [u8; 32])>,
    bytes: usize,
    max_bytes: usize,
    max_entries: usize,
}

impl Default for AssetCache {
    fn default() -> Self {
        Self {
            by_key: HashMap::new(),
            by_hash: HashMap::new(),
            order: VecDeque::new(),
            bytes: 0,
            max_bytes: 128 * 1024 * 1024,
            max_entries: 4096,
        }
    }
}

impl AssetCache {
    fn matching_key(&self, hash: &[u8; 32], data: &[u8]) -> Option<String> {
        let key = self.by_hash.get(hash)?;
        // Hashes are only an index: compare the full bytes before reusing data.
        (self.by_key.get(key)?.as_ref() == data).then(|| key.clone())
    }

    fn insert(&mut self, key: String, hash: [u8; 32], data: Bytes) {
        if data.len() > self.max_bytes || self.by_key.contains_key(&key) {
            return;
        }
        while self.bytes + data.len() > self.max_bytes || self.by_key.len() >= self.max_entries {
            let Some((old_key, old_hash)) = self.order.pop_front() else {
                break;
            };
            if let Some(old) = self.by_key.remove(&old_key) {
                self.bytes -= old.len();
            }
            if self.by_hash.get(&old_hash) == Some(&old_key) {
                self.by_hash.remove(&old_hash);
            }
        }
        self.bytes += data.len();
        self.order.push_back((key.clone(), hash));
        self.by_hash.insert(hash, key.clone());
        self.by_key.insert(key, data);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn asset_cache_checks_bytes_and_bounds_memory_and_entries() {
        let mut cache = AssetCache {
            max_bytes: 5,
            max_entries: 2,
            ..Default::default()
        };
        cache.insert("a".into(), [1; 32], Bytes::from_static(b"aa"));
        assert_eq!(cache.matching_key(&[1; 32], b"aa"), Some("a".into()));
        assert_eq!(cache.matching_key(&[1; 32], b"bb"), None);
        cache.insert("b".into(), [1; 32], Bytes::from_static(b"bb"));
        cache.insert("c".into(), [2; 32], Bytes::from_static(b"cc"));
        assert!(!cache.by_key.contains_key("a"));
        assert_eq!(cache.matching_key(&[1; 32], b"bb"), Some("b".into()));
        cache.insert("d".into(), [3; 32], Bytes::from_static(b"dddd"));
        assert_eq!(cache.bytes, 4);
        assert_eq!(cache.by_key.len(), 1);
        cache.insert("huge".into(), [4; 32], Bytes::from_static(b"123456"));
        assert_eq!(cache.bytes, 4);
        assert!(!cache.by_key.contains_key("huge"));
    }
}
