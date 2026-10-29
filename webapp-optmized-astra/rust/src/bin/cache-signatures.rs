//! Generate a private signature cache from actual image owners and current keys.
use anyhow::{Result, ensure};
use chrono::Utc;
use futures_util::TryStreamExt;
use isureal::{
    config::config_from_env,
    image_signature::{IMAGE_URL_LIFETIME, image_signature},
    signature_file::{MAGIC, RECORD_BYTES},
};
use sqlx::{Connection, PgConnection, Row};
use std::{
    fs::OpenOptions,
    io::{BufWriter, Write},
    os::unix::fs::OpenOptionsExt,
};

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<()> {
    let path = std::env::args().nth(1).expect("output file required");
    ensure!(
        std::env::var("ISUREAL_SIGNATURE_FILE")
            .unwrap_or_default()
            .is_empty(),
        "generator must compute from source rows"
    );
    let expires = Utc::now().timestamp() + IMAGE_URL_LIFETIME.num_seconds();
    let mut connection = PgConnection::connect(&config_from_env().database_dsn()).await?;
    let mut rows = sqlx::query(
        "SELECT i.id, u.secret_key_hash FROM image_objects i JOIN users u ON u.id=i.owner_user_id",
    )
    .fetch(&mut connection);
    let mut records: Vec<[u8; RECORD_BYTES]> = Vec::new();
    while let Some(row) = rows.try_next().await? {
        let image_id: String = row.try_get("id")?;
        let id = uuid::Uuid::parse_str(&image_id)?;
        ensure!(id.to_string() == image_id, "noncanonical source image ID");
        let key = hex::decode(row.try_get::<String, _>("secret_key_hash")?)?;
        ensure!(key.len() == 32, "invalid source signing key");
        let mut record = [0; RECORD_BYTES];
        record[..32].copy_from_slice(&key);
        record[32..48].copy_from_slice(id.as_bytes());
        record[48..].copy_from_slice(&image_signature(&key, &image_id, expires));
        records.push(record);
        if records.len().is_multiple_of(100000) {
            eprintln!("computed {} signatures", records.len());
        }
    }
    records.sort_unstable_by(|a, b| a[..48].cmp(&b[..48]));
    records.dedup_by(|a, b| a[..48] == b[..48]);
    let mut file = BufWriter::new(
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(path)?,
    );
    file.write_all(MAGIC)?;
    file.write_all(&expires.to_le_bytes())?;
    for record in &records {
        file.write_all(record)?;
    }
    file.flush()?;
    println!(
        "rows={} bytes={} expires={expires}",
        records.len(),
        16 + records.len() * RECORD_BYTES
    );
    Ok(())
}
