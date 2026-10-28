//! Offline migration helper. The deployment controller stops all producers and
//! verifies identical archive/manifest hashes on all three readers before apply.
use anyhow::{Context, Result, ensure};
use chrono::{DateTime, Utc};
use futures_util::TryStreamExt;
use isureal::{archive_store::ArchiveReference, config::config_from_env};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::{Connection, PgConnection, Row};
use std::{
    fs::{self, File, OpenOptions},
    io::{BufRead, BufReader, Read, Write},
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
};

#[derive(Serialize, Deserialize)]
struct Report {
    partition: String,
    archive: String,
    rows: u64,
    bytes: u64,
    sha256: String,
    manifest_sha256: String,
}

fn hash_file(path: &Path) -> Result<String> {
    let mut input = File::open(path)?;
    let mut hasher = Sha256::new();
    let mut buffer = [0; 65536];
    loop {
        let size = input.read(&mut buffer)?;
        if size == 0 {
            break;
        }
        hasher.update(&buffer[..size]);
    }
    Ok(hex::encode(hasher.finalize()))
}

fn validate_manifest(path: &Path, report: &Report) -> Result<()> {
    ensure!(
        hash_file(path)? == report.manifest_sha256,
        "manifest hash mismatch"
    );
    let mut rows = 0;
    let mut end = 0;
    let mut previous = None;
    for line in BufReader::new(File::open(path)?).lines() {
        let line = line?;
        let fields: Vec<_> = line.split('\t').collect();
        ensure!(fields.len() == 5, "invalid manifest columns");
        ensure!(
            uuid::Uuid::parse_str(fields[0])?.to_string() == fields[0],
            "noncanonical image ID"
        );
        ensure!(
            fields[4].parse::<u32>()?.to_string() == fields[4],
            "invalid source version"
        );
        let timestamp = DateTime::parse_from_rfc3339(fields[1])?;
        let order = (fields[0].to_owned(), timestamp);
        ensure!(
            previous.as_ref().is_none_or(|old| old < &order),
            "manifest must have unique sorted rows"
        );
        previous = Some(order);
        let reference = ArchiveReference::parse(fields[2]).context("invalid archive reference")?;
        ensure!(
            reference.archive == report.archive && reference.offset == end,
            "noncontiguous archive"
        );
        ensure!(
            reference.length.to_string() == fields[3],
            "image length mismatch"
        );
        end += reference.length as u64;
        rows += 1;
    }
    ensure!(
        rows == report.rows && end == report.bytes && rows > 0,
        "incomplete archive manifest"
    );
    Ok(())
}

async fn build(
    db: &mut PgConnection,
    partition: &str,
    archive: &str,
    root: &Path,
    manifest: &Path,
) -> Result<Report> {
    let packs = root.join("packs");
    fs::create_dir_all(&packs)?;
    let target = packs.join(archive);
    ensure!(
        !target.exists(),
        "archive already exists; use verify to inspect it"
    );
    let temporary = packs.join(format!(".{archive}.tmp"));
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o444)
        .open(&temporary)?;
    let mut index = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(manifest)?;
    let mut report = Report {
        partition: partition.into(),
        archive: archive.into(),
        rows: 0,
        bytes: 0,
        sha256: String::new(),
        manifest_sha256: String::new(),
    };
    let mut hash = Sha256::new();
    let mut tx = db.begin().await?;
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
        .execute(&mut *tx)
        .await?;
    let query = format!(
        "SELECT id,created_at,data,xmin::text AS version FROM {partition} WHERE asset_key IS NULL AND created_at<=(SELECT created_at FROM initial_data_boundary) ORDER BY id,created_at"
    );
    let mut rows = sqlx::query(&query).fetch(&mut *tx);
    while let Some(row) = rows.try_next().await? {
        let id: String = row.try_get("id")?;
        let timestamp: DateTime<Utc> = row.try_get("created_at")?;
        let bytes: Vec<u8> = row.try_get("data")?;
        let version: String = row.try_get("version")?;
        let key = format!("pack:{archive}:{}:{}", report.bytes, bytes.len());
        ensure!(
            ArchiveReference::parse(&key).is_some(),
            "archive or image too large, or empty image"
        );
        output.write_all(&bytes)?;
        hash.update(&bytes);
        writeln!(
            index,
            "{id}\t{}\t{key}\t{}\t{version}",
            timestamp.to_rfc3339(),
            bytes.len()
        )?;
        report.rows += 1;
        report.bytes += bytes.len() as u64;
    }
    drop(rows);
    tx.commit().await?;
    ensure!(
        report.rows > 0,
        "partition has no unmigrated initial images"
    );
    output.sync_all()?;
    index.sync_all()?;
    report.sha256 = hex::encode(hash.finalize());
    report.manifest_sha256 = hash_file(manifest)?;
    validate_manifest(manifest, &report)?;
    fs::hard_link(&temporary, &target)?;
    fs::remove_file(&temporary)?;
    File::open(&packs)?.sync_all()?;
    Ok(report)
}

async fn apply(db: &mut PgConnection, manifest: &Path, report: &Report) -> Result<()> {
    validate_manifest(manifest, report)?;
    ensure!(
        !sqlx::query_scalar::<_, bool>("SELECT pg_is_in_recovery()")
            .fetch_one(&mut *db)
            .await?,
        "apply requires primary"
    );
    let mut tx = db.begin().await?;
    sqlx::query("SET LOCAL synchronous_commit=remote_apply")
        .execute(&mut *tx)
        .await?;
    sqlx::query(&format!(
        "LOCK TABLE {} IN ACCESS EXCLUSIVE MODE",
        report.partition
    ))
    .execute(&mut *tx)
    .await?;
    sqlx::query("CREATE TEMP TABLE astra_archive_manifest(id text,created_at timestamptz,asset_key text,bytes bigint,version text,PRIMARY KEY(id,created_at)) ON COMMIT DROP").execute(&mut *tx).await?;
    let mut copy = tx
        .copy_in_raw("COPY astra_archive_manifest FROM STDIN")
        .await?;
    let mut input = File::open(manifest)?;
    let mut buffer = [0; 65536];
    loop {
        let size = input.read(&mut buffer)?;
        if size == 0 {
            break;
        }
        copy.send(&buffer[..size]).await?;
    }
    ensure!(
        copy.finish().await? == report.rows,
        "manifest COPY row count mismatch"
    );
    let query = format!(
        "UPDATE {} i SET data=''::bytea,asset_key=m.asset_key FROM astra_archive_manifest m WHERE i.id=m.id AND i.created_at=m.created_at AND i.asset_key IS NULL AND octet_length(i.data)=m.bytes AND i.xmin::text=m.version AND i.created_at<=(SELECT created_at FROM initial_data_boundary)",
        report.partition
    );
    let changed = sqlx::query(&query).execute(&mut *tx).await?.rows_affected();
    ensure!(
        changed == report.rows,
        "source changed since archive build; transaction rolled back"
    );
    tx.commit().await?;
    Ok(())
}

async fn restore(
    db: &mut PgConnection,
    manifest: &Path,
    report: &Report,
    packs: &Path,
) -> Result<()> {
    validate_manifest(manifest, report)?;
    ensure!(
        hash_file(&packs.join(&report.archive))? == report.sha256,
        "restore archive hash mismatch"
    );
    let mut tx = db.begin().await?;
    sqlx::query("SET LOCAL synchronous_commit=remote_apply")
        .execute(&mut *tx)
        .await?;
    sqlx::query(&format!(
        "LOCK TABLE {} IN ACCESS EXCLUSIVE MODE",
        report.partition
    ))
    .execute(&mut *tx)
    .await?;
    let mut input = BufReader::new(File::open(manifest)?).lines();
    loop {
        let mut batch = Vec::new();
        for line in input.by_ref().take(256) {
            let line = line?;
            let fields: Vec<_> = line.split('\t').collect();
            let reference = ArchiveReference::parse(fields[2]).context("restore reference")?;
            batch.push((
                fields[0].to_owned(),
                DateTime::parse_from_rfc3339(fields[1])?.with_timezone(&Utc),
                reference.read(packs)?,
                fields[2].to_owned(),
            ));
        }
        if batch.is_empty() {
            break;
        }
        let mut query = sqlx::QueryBuilder::<sqlx::Postgres>::new(format!(
            "UPDATE {} i SET data=v.data,asset_key=NULL FROM (",
            report.partition
        ));
        query.push_values(&batch, |mut row, (id, time, data, key)| {
            row.push_bind(id)
                .push_bind(time)
                .push_bind(data)
                .push_bind(key);
        });
        query.push(") v(id,created_at,data,asset_key) WHERE i.id=v.id AND i.created_at=v.created_at AND i.asset_key=v.asset_key AND octet_length(i.data)=0");
        ensure!(
            query.build().execute(&mut *tx).await?.rows_affected() == batch.len() as u64,
            "restore source changed; rollback"
        );
    }
    tx.commit().await?;
    Ok(())
}

async fn verify_source(db: &mut PgConnection, report: &Report) -> Result<()> {
    let query = format!(
        "SELECT data FROM {} WHERE asset_key IS NULL AND created_at<=(SELECT created_at FROM initial_data_boundary) ORDER BY id,created_at",
        report.partition
    );
    let mut rows = sqlx::query(&query).fetch(db);
    let mut hash = Sha256::new();
    let mut count = 0;
    let mut length = 0;
    while let Some(row) = rows.try_next().await? {
        let bytes: Vec<u8> = row.try_get("data")?;
        count += 1;
        length += bytes.len() as u64;
        hash.update(bytes);
    }
    ensure!(
        count == report.rows
            && length == report.bytes
            && hex::encode(hash.finalize()) == report.sha256,
        "restored SQL contents do not match original archive"
    );
    Ok(())
}

#[tokio::main(worker_threads = 2)]
async fn main() -> Result<()> {
    let args: Vec<_> = std::env::args().collect();
    ensure!(
        args.len() == 4,
        "expected build|verify|apply|restore|verify-source PARTITION ARCHIVE_UUID_HEX"
    );
    let (mode, partition, archive) = (&args[1], &args[2], &args[3]);
    ensure!(
        regex::Regex::new(r"^image_objects_202[4-7](0[1-9]|1[0-2])$")?.is_match(partition),
        "invalid image partition"
    );
    ensure!(
        uuid::Uuid::parse_str(archive)?.simple().to_string() == *archive,
        "invalid archive ID"
    );
    let root = PathBuf::from("/var/lib/astra-images");
    let directory = PathBuf::from("/var/lib/astra-maint/archives");
    fs::create_dir_all(&directory)?;
    let manifest = directory.join(format!("{archive}.tsv"));
    let report_path = directory.join(format!("{archive}.json"));
    if mode == "build" {
        let mut db = PgConnection::connect(&config_from_env().database_dsn()).await?;
        let report = build(&mut db, partition, archive, &root, &manifest).await?;
        let bytes = serde_json::to_vec(&report)?;
        let mut output = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&report_path)?;
        output.write_all(&bytes)?;
        output.sync_all()?;
        File::open(&directory)?.sync_all()?;
        println!("{}", String::from_utf8(bytes)?);
    } else {
        let report: Report = serde_json::from_reader(File::open(&report_path)?)?;
        ensure!(
            report.partition == *partition && report.archive == *archive,
            "wrong migration report"
        );
        validate_manifest(&manifest, &report)?;
        match mode.as_str() {
            "verify" => {
                let path = root.join("packs").join(archive);
                ensure!(
                    fs::metadata(&path)?.len() == report.bytes
                        && hash_file(&path)? == report.sha256,
                    "archive contents differ"
                );
            }
            "apply" => {
                let mut db = PgConnection::connect(&config_from_env().database_dsn()).await?;
                apply(&mut db, &manifest, &report).await?;
            }
            "restore" => {
                let mut db = PgConnection::connect(&config_from_env().database_dsn()).await?;
                restore(&mut db, &manifest, &report, &root.join("packs")).await?;
            }
            "verify-source" => {
                let mut db = PgConnection::connect(&config_from_env().database_dsn()).await?;
                verify_source(&mut db, &report).await?;
            }
            _ => anyhow::bail!("unknown mode"),
        }
        println!("{}", serde_json::to_string(&report)?);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn migration_preserves_initial_bytes_and_rolls_back_if_source_changed() -> Result<()> {
        let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
            return Ok(());
        };
        let mut db = PgConnection::connect(&dsn).await?;
        let schema = format!("pack_test_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&mut db)
            .await?;
        sqlx::query(&format!("SET search_path TO {schema}"))
            .execute(&mut db)
            .await?;
        sqlx::raw_sql("CREATE TABLE initial_data_boundary(created_at timestamptz); INSERT INTO initial_data_boundary VALUES ('2025-10-31'); CREATE TABLE image_objects_202510(id text,created_at timestamptz,data bytea NOT NULL,asset_key text,PRIMARY KEY(id,created_at));").execute(&mut db).await?;
        let images = [
            include_bytes!("../../tests/fixtures/small.jpg").as_slice(),
            include_bytes!("../../tests/fixtures/wide.jpg").as_slice(),
        ];
        let ids = [
            uuid::Uuid::new_v4().to_string(),
            uuid::Uuid::new_v4().to_string(),
        ];
        for (id, bytes) in ids.iter().zip(images) {
            sqlx::query("INSERT INTO image_objects_202510 VALUES ($1,'2025-10-01',$2,NULL)")
                .bind(id)
                .bind(bytes)
                .execute(&mut db)
                .await?;
        }
        sqlx::query("INSERT INTO image_objects_202510 VALUES ($1,'2025-11-01',$2,NULL)")
            .bind(uuid::Uuid::new_v4().to_string())
            .bind(images[0])
            .execute(&mut db)
            .await?;
        let root = std::env::temp_dir().join(format!("astra-pack-build-{}", uuid::Uuid::new_v4()));
        fs::create_dir(&root)?;
        let archive = uuid::Uuid::new_v4().simple().to_string();
        let manifest = root.join("index.tsv");
        let report = build(&mut db, "image_objects_202510", &archive, &root, &manifest).await?;
        assert_eq!(report.rows, 2);
        assert_eq!(
            report.sha256,
            hash_file(&root.join("packs").join(&archive))?
        );
        // A later source edit must abort the entire metadata publication.
        sqlx::query("UPDATE image_objects_202510 SET data='changed'::bytea WHERE id=$1")
            .bind(&ids[0])
            .execute(&mut db)
            .await?;
        assert!(apply(&mut db, &manifest, &report).await.is_err());
        assert_eq!(
            sqlx::query_scalar::<_, i64>(
                "SELECT count(*) FROM image_objects_202510 WHERE asset_key IS NOT NULL"
            )
            .fetch_one(&mut db)
            .await?,
            0
        );
        sqlx::query("UPDATE image_objects_202510 SET data=$2 WHERE id=$1")
            .bind(&ids[0])
            .bind(images[0])
            .execute(&mut db)
            .await?;
        // Restoring bytes also changes xmin; rebuild before publication.
        assert!(apply(&mut db, &manifest, &report).await.is_err());
        let archive = uuid::Uuid::new_v4().simple().to_string();
        let manifest = root.join("rebuilt.tsv");
        let report = build(&mut db, "image_objects_202510", &archive, &root, &manifest).await?;
        apply(&mut db, &manifest, &report).await?;
        for (id, expected) in ids.iter().zip(images) {
            let (key, inline): (String, Vec<u8>) =
                sqlx::query_as("SELECT asset_key,data FROM image_objects_202510 WHERE id=$1")
                    .bind(id)
                    .fetch_one(&mut db)
                    .await?;
            assert!(inline.is_empty());
            assert_eq!(
                ArchiveReference::parse(&key)
                    .unwrap()
                    .read(&root.join("packs"))?,
                expected
            );
        }
        assert_eq!(sqlx::query_scalar::<_,i64>("SELECT count(*) FROM image_objects_202510 WHERE created_at>(SELECT created_at FROM initial_data_boundary) AND asset_key IS NULL").fetch_one(&mut db).await?,1);
        // Repeating apply cannot accidentally publish a different mapping.
        assert!(apply(&mut db, &manifest, &report).await.is_err());
        restore(&mut db, &manifest, &report, &root.join("packs")).await?;
        verify_source(&mut db, &report).await?;
        for (id, expected) in ids.iter().zip(images) {
            let (bytes, key): (Vec<u8>, Option<String>) =
                sqlx::query_as("SELECT data,asset_key FROM image_objects_202510 WHERE id=$1")
                    .bind(id)
                    .fetch_one(&mut db)
                    .await?;
            assert_eq!(bytes, expected);
            assert!(key.is_none());
        }
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&mut db)
            .await?;
        fs::remove_dir_all(root)?;
        Ok(())
    }
}
