use anyhow::{Result, ensure};
use futures_util::{StreamExt, TryStreamExt, stream};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{MySqlPool, PgPool, mysql::MySqlPoolOptions, postgres::PgPoolOptions};
use std::{sync::Arc, time::Instant};

#[derive(Clone)]
enum Engine {
    Postgres(PgPool),
    Mysql(MySqlPool),
}

impl Engine {
    async fn initialize(&self) -> Result<()> {
        match self {
            Self::Postgres(db) => {
                sqlx::query("DROP TABLE IF EXISTS astra_blob_compare")
                    .execute(db)
                    .await?;
                sqlx::query("CREATE TABLE astra_blob_compare (id varchar(36) COLLATE \"C\" PRIMARY KEY,data bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp())").execute(db).await?;
                sqlx::query(
                    "ALTER TABLE astra_blob_compare ALTER COLUMN data SET STORAGE EXTERNAL",
                )
                .execute(db)
                .await?;
            }
            Self::Mysql(db) => {
                sqlx::query("DROP TABLE IF EXISTS astra_blob_compare")
                    .execute(db)
                    .await?;
                sqlx::query("CREATE TABLE astra_blob_compare (id varchar(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,data longblob NOT NULL,created_at timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)) ENGINE=InnoDB ROW_FORMAT=DYNAMIC").execute(db).await?;
            }
        }
        Ok(())
    }
    async fn write(&self, id: &str, data: &[u8]) -> Result<()> {
        match self {
            Self::Postgres(db) => {
                sqlx::query("INSERT INTO astra_blob_compare(id,data) VALUES ($1,$2)")
                    .bind(id)
                    .bind(data)
                    .execute(db)
                    .await?;
            }
            Self::Mysql(db) => {
                sqlx::query("INSERT INTO astra_blob_compare(id,data) VALUES (?,?)")
                    .bind(id)
                    .bind(data)
                    .execute(db)
                    .await?;
            }
        }
        Ok(())
    }
    async fn read(&self, id: &str) -> Result<Vec<u8>> {
        Ok(match self {
            Self::Postgres(db) => {
                sqlx::query_scalar("SELECT data FROM astra_blob_compare WHERE id=$1")
                    .bind(id)
                    .fetch_one(db)
                    .await?
            }
            Self::Mysql(db) => {
                sqlx::query_scalar("SELECT data FROM astra_blob_compare WHERE id=?")
                    .bind(id)
                    .fetch_one(db)
                    .await?
            }
        })
    }
    async fn wal_bytes(&self) -> Result<u64> {
        Ok(match self {
            Self::Postgres(db) => {
                sqlx::query_scalar::<_, i64>(
                    "SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(),'0/0')::bigint",
                )
                .fetch_one(db)
                .await? as u64
            }
            Self::Mysql(db) => sqlx::query_as::<_, (String, String)>(
                "SHOW GLOBAL STATUS LIKE 'Innodb_os_log_written'",
            )
            .fetch_one(db)
            .await?
            .1
            .parse()?,
        })
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();
    ensure!(args.len() == 6, "engine write|read rows bytes concurrency");
    let engine = &args[1];
    let phase = &args[2];
    let rows: usize = args[3].parse()?;
    let bytes: usize = args[4].parse()?;
    let concurrency: usize = args[5].parse()?;
    ensure!(
        matches!(phase.as_str(), "write" | "read")
            && (1..=4096).contains(&rows)
            && (8..=1048576).contains(&bytes)
            && (1..=32).contains(&concurrency),
        "invalid bounded workload"
    );
    ensure!(
        rows * bytes <= 512 * 1024 * 1024,
        "client dataset too large"
    );
    let (db, version) = match engine.as_str() {
        "postgres" => {
            let db = PgPoolOptions::new()
                .max_connections(32)
                .test_before_acquire(false)
                .connect("postgres://postgres@127.0.0.1:26216/astra_engine")
                .await?;
            let safe:bool=sqlx::query_scalar("SELECT current_setting('fsync')='on' AND current_setting('synchronous_commit')='on' AND current_setting('full_page_writes')='on'").fetch_one(&db).await?;
            ensure!(safe, "PostgreSQL durability must stay enabled");
            let version: String = sqlx::query_scalar("SELECT version()")
                .fetch_one(&db)
                .await?;
            (Engine::Postgres(db), version)
        }
        "mysql" => {
            let db = MySqlPoolOptions::new()
                .max_connections(32)
                .test_before_acquire(false)
                .connect("mysql://root@127.0.0.1:26215/astra_engine")
                .await?;
            let (flush, doublewrite, binlog): (u64, String, i64) = sqlx::query_as(
                "SELECT @@innodb_flush_log_at_trx_commit,@@innodb_doublewrite,@@log_bin",
            )
            .fetch_one(&db)
            .await?;
            ensure!(
                flush == 1 && doublewrite == "ON" && binlog == 0,
                "standalone InnoDB durability must stay enabled"
            );
            let version: String = sqlx::query_scalar("SELECT VERSION()")
                .fetch_one(&db)
                .await?;
            (Engine::Mysql(db), version)
        }
        _ => anyhow::bail!("unknown engine"),
    };
    let mut random = 0x9e3779b97f4a7c15_u64;
    let base: Vec<u8> = (0..bytes)
        .map(|_| {
            random ^= random << 13;
            random ^= random >> 7;
            random ^= random << 17;
            random as u8
        })
        .collect();
    let data: Arc<Vec<(String, Vec<u8>)>> = Arc::new(
        (0..rows)
            .map(|index| {
                let hash = Sha256::digest((index as u64).to_le_bytes());
                let id = uuid::Uuid::from_bytes(hash[..16].try_into().unwrap()).to_string();
                let mut data = base.clone();
                data[..8].copy_from_slice(&(index as u64).to_le_bytes());
                (id, data)
            })
            .collect(),
    );
    if phase == "write" {
        db.initialize().await?;
    }
    let wal_before = db.wal_bytes().await?;
    let start = Instant::now();
    let mut latencies: Vec<f64> = stream::iter(0..rows)
        .map(|i| {
            let db = db.clone();
            let data = data.clone();
            async move {
                let (id, expected) = &data[i];
                let start = Instant::now();
                if phase == "write" {
                    db.write(id, expected).await?;
                } else {
                    ensure!(
                        &db.read(id).await? == expected,
                        "image bytes changed for row {i}"
                    );
                }
                Ok::<_, anyhow::Error>(start.elapsed().as_secs_f64() * 1000.)
            }
        })
        .buffer_unordered(concurrency)
        .try_collect()
        .await?;
    let elapsed = start.elapsed().as_secs_f64();
    let wal_after = db.wal_bytes().await?;
    latencies.sort_by(f64::total_cmp);
    let percentile = |p: f64| latencies[((rows - 1) as f64 * p).round() as usize];
    println!(
        "{}",
        json!({"engine":engine,"version":version,"phase":phase,"rows":rows,"bytes_per_image":bytes,"concurrency":concurrency,"elapsed_seconds":elapsed,"ops_per_second":rows as f64/elapsed,"mib_per_second":(rows*bytes) as f64/1048576./elapsed,"p50_ms":percentile(0.5),"p90_ms":percentile(0.9),"p99_ms":percentile(0.99),"wal_or_redo_bytes_observed":wal_after.saturating_sub(wal_before),"all_bytes_verified":phase=="read"})
    );
    Ok(())
}
