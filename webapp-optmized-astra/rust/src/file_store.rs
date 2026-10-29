//! Immutable image files. Publish metadata only after file and directory fsync.
use bytes::Bytes;
use hyper_client::{Body, Client, body::HttpBody, client::HttpConnector};
use std::{
    collections::HashMap,
    fs::{self, File, OpenOptions},
    io::{self, Read, Write},
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
    sync::Arc,
    time::Duration,
};
use tokio::sync::Semaphore;

const MAX_IMAGE_BYTES: usize = 32 * 1024 * 1024;

#[derive(Clone)]
pub struct FileStore {
    root: Arc<PathBuf>,
    region: String,
    targets: Arc<HashMap<String, String>>,
    pub writes_enabled: bool,
    archives_enabled: bool,
    archive_peer: Option<std::net::SocketAddr>,
    writers: Arc<Semaphore>,
    client: Client<HttpConnector, Body>,
}

fn valid_region(region: &str) -> bool {
    matches!(region, "asia" | "europe" | "america")
}
fn valid_id(id: &str) -> bool {
    uuid::Uuid::parse_str(id).is_ok_and(|uuid| uuid.to_string() == id)
}
fn failure(message: &'static str) -> sqlx::Error {
    sqlx::Error::Protocol(message.into())
}
fn io_failure(error: io::Error) -> sqlx::Error {
    if error.kind() == io::ErrorKind::NotFound {
        sqlx::Error::RowNotFound
    } else {
        sqlx::Error::Io(error)
    }
}

struct UnpublishedFile(Option<PathBuf>);
impl Drop for UnpublishedFile {
    fn drop(&mut self) {
        if let Some(path) = &self.0 {
            let _ = fs::remove_file(path);
        }
    }
}

impl FileStore {
    pub fn new(
        root: PathBuf,
        region: String,
        targets: HashMap<String, String>,
        writes_enabled: bool,
    ) -> Self {
        assert!(root.is_absolute() && valid_region(&region));
        assert!(
            targets.iter().all(|(region, host)| valid_region(region)
                && host.parse::<std::net::SocketAddr>().is_ok())
        );
        let mut connector = HttpConnector::new();
        connector.set_nodelay(true);
        connector.set_connect_timeout(Some(Duration::from_secs(2)));
        Self {
            root: Arc::new(root),
            region,
            targets: Arc::new(targets),
            writes_enabled,
            archives_enabled: false,
            archive_peer: None,
            writers: Arc::new(Semaphore::new(16)),
            client: Client::builder().pool_max_idle_per_host(8).build(connector),
        }
    }

    pub fn with_archives(mut self, enabled: bool) -> Self {
        self.archives_enabled = enabled;
        self
    }

    pub fn with_archive_peer(mut self, peer: Option<std::net::SocketAddr>) -> Self {
        self.archive_peer = peer;
        self
    }

    pub async fn prepare(&self) -> io::Result<()> {
        let root = self.root.clone();
        tokio::task::spawn_blocking(move || {
            fs::create_dir_all(root.as_ref())?;
            for bucket in 0..256 {
                fs::create_dir_all(root.join(format!("{bucket:02x}")))?;
            }
            fs::create_dir_all(root.join("packs"))?;
            File::open(root.as_ref())?.sync_all()?;
            if let Some(parent) = root.parent() {
                File::open(parent)?.sync_all()?;
            }
            Ok::<_, io::Error>(())
        })
        .await
        .map_err(io::Error::other)?
    }

    pub async fn from_env(region: &str) -> Option<Self> {
        let root = std::env::var("ISUREAL_FILE_IMAGE_ROOT").unwrap_or_default();
        let writes = std::env::var("ISUREAL_FILE_IMAGE_WRITES").unwrap_or_else(|_| "0".into());
        assert!(matches!(writes.as_str(), "0" | "1"));
        if root.is_empty() {
            assert_eq!(writes, "0");
            return None;
        }
        assert_eq!(root, "/var/lib/astra-images");
        let targets: HashMap<String, String> = serde_json::from_str(
            &std::env::var("ISUREAL_FILE_IMAGE_HOSTS_JSON").expect("file image peers configured"),
        )
        .expect("file image peer mapping");
        assert_eq!(targets.len(), 3);
        for (region, host) in &targets {
            assert!(matches!(
                (region.as_str(), host.as_str()),
                ("asia", "192.168.177.10:8082")
                    | ("europe", "192.168.178.10:8082")
                    | ("america", "192.168.179.10:8082")
            ));
        }
        let archives = std::env::var("ISUREAL_FILE_IMAGE_ARCHIVES").unwrap_or_else(|_| "0".into());
        assert!(matches!(archives.as_str(), "0" | "1"));
        let peer = std::env::var("ISUREAL_ARCHIVE_PEER").unwrap_or_default();
        let peer = if peer.is_empty() {
            None
        } else {
            assert_eq!(region, "asia");
            assert_eq!(peer, "192.168.180.10:8082");
            assert_eq!(archives, "1");
            Some(peer.parse().expect("private archive peer address"))
        };
        let store = Self::new(root.into(), region.into(), targets, writes == "1")
            .with_archives(archives == "1")
            .with_archive_peer(peer);
        store
            .prepare()
            .await
            .expect("durable file image directory available");
        Some(store)
    }

    pub fn has_region(&self, region: &str) -> bool {
        self.is_local(region) || self.targets.contains_key(region)
    }

    pub fn is_local(&self, region: &str) -> bool {
        self.region == region
    }

    pub async fn put(&self, id: &str, data: &Bytes) -> Result<String, sqlx::Error> {
        if !self.writes_enabled || !valid_id(id) || data.is_empty() || data.len() > MAX_IMAGE_BYTES
        {
            return Err(failure("invalid file image write"));
        }
        let directory = self.root.join(&id[..2]);
        let target = directory.join(id);
        let data = data.clone();
        let permit = self
            .writers
            .clone()
            .acquire_owned()
            .await
            .map_err(|_| failure("file writer closed"))?;
        tokio::task::spawn_blocking(move || {
            let _permit = permit;
            // SQL metadata is the authority for public visibility. The key is
            // returned only after both the new file and directory are durable.
            // create_new refuses collisions without touching an existing file.
            let mut file = OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o444)
                .open(&target)?;
            // Arm cleanup only after this call has created its own file.
            let mut unpublished = UnpublishedFile(Some(target));
            file.write_all(&data)?;
            file.sync_all()?;
            File::open(&directory)?.sync_all()?;
            unpublished.0 = None;
            Ok::<_, io::Error>(())
        })
        .await
        .map_err(|error| sqlx::Error::Io(io::Error::other(error)))?
        .map_err(io_failure)?;
        Ok(format!("fs:{}:{id}", self.region))
    }

    pub async fn archived_images(
        &self,
        keys: &[String],
    ) -> Result<HashMap<String, bytes::Bytes>, sqlx::Error> {
        if self.archive_peer.is_some() {
            return Ok(HashMap::new());
        }
        let mut packed_keys = Vec::new();
        let mut references = Vec::new();
        for key in keys.iter().filter(|key| key.starts_with("pack:")) {
            if !self.archives_enabled {
                return Err(failure("archive image readers disabled"));
            }
            references.push(
                crate::archive_store::ArchiveReference::parse(key)
                    .ok_or_else(|| failure("invalid image archive reference"))?,
            );
            packed_keys.push(key.clone());
        }
        if references.is_empty() {
            return Ok(HashMap::new());
        }
        let directory = self.root.join("packs");
        tokio::task::spawn_blocking(move || {
            let images = crate::archive_store::read_many(&directory, &references)?;
            Ok::<_, io::Error>(packed_keys.into_iter().zip(images).collect())
        })
        .await
        .map_err(|error| sqlx::Error::Io(io::Error::other(error)))?
        .map_err(io_failure)
    }

    pub async fn get(&self, key: &str) -> Result<Vec<u8>, sqlx::Error> {
        if key.starts_with("pack:") {
            if !self.archives_enabled {
                return Err(failure("archive image readers disabled"));
            }
            let reference = crate::archive_store::ArchiveReference::parse(key)
                .ok_or_else(|| failure("invalid image archive reference"))?;
            if let Some(peer) = self.archive_peer {
                return crate::archive_peer::read(&self.client, peer, &reference).await;
            }
            let directory = self.root.join("packs");
            return tokio::task::spawn_blocking(move || reference.read(&directory))
                .await
                .map_err(|error| sqlx::Error::Io(io::Error::other(error)))?
                .map_err(io_failure);
        }
        let Some((region, id)) = key.strip_prefix("fs:").and_then(|key| key.split_once(':')) else {
            return Err(failure("invalid file image reference"));
        };
        if !valid_region(region) || !valid_id(id) {
            return Err(failure("invalid file image reference"));
        }
        if self.is_local(region) {
            let path = self.root.join(&id[..2]).join(id);
            return tokio::task::spawn_blocking(move || read_bounded(&path))
                .await
                .map_err(|error| sqlx::Error::Io(io::Error::other(error)))?
                .map_err(io_failure);
        }
        let host = self
            .targets
            .get(region)
            .ok_or_else(|| failure("missing file image peer"))?;
        let uri = format!("http://{host}/internal/astra/files/{}/{id}", &id[..2])
            .parse()
            .map_err(|_| failure("invalid file image peer URI"))?;
        tokio::time::timeout(Duration::from_secs(10), async {
            let mut response = self
                .client
                .get(uri)
                .await
                .map_err(|error| sqlx::Error::Io(io::Error::other(error)))?;
            if response.status() == hyper_client::StatusCode::NOT_FOUND {
                return Err(sqlx::Error::RowNotFound);
            }
            if response.status() != hyper_client::StatusCode::OK {
                return Err(failure("file image peer rejected read"));
            }
            let mut data = Vec::new();
            while let Some(chunk) = response.body_mut().data().await {
                let chunk = chunk.map_err(|error| sqlx::Error::Io(io::Error::other(error)))?;
                if data.len() + chunk.len() > MAX_IMAGE_BYTES {
                    return Err(failure("oversized file image response"));
                }
                data.extend_from_slice(&chunk);
            }
            if data.is_empty() {
                return Err(failure("empty file image response"));
            }
            Ok(data)
        })
        .await
        .map_err(|_| {
            sqlx::Error::Io(io::Error::new(
                io::ErrorKind::TimedOut,
                "file image read timed out",
            ))
        })?
    }
}

fn read_bounded(path: &Path) -> io::Result<Vec<u8>> {
    let file = File::open(path)?;
    let len = file.metadata()?.len();
    if len == 0 || len > MAX_IMAGE_BYTES as u64 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid image file length",
        ));
    }
    let mut data = Vec::with_capacity(len as usize);
    std::io::Read::take(file, (MAX_IMAGE_BYTES + 1) as u64).read_to_end(&mut data)?;
    if data.len() != len as usize {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "image file changed",
        ));
    }
    Ok(data)
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        Router,
        extract::{Path as AxumPath, State},
        http::StatusCode,
        response::IntoResponse,
        routing::get,
    };

    fn root() -> PathBuf {
        std::env::temp_dir().join(format!("astra-file-test-{}", uuid::Uuid::new_v4()))
    }

    #[tokio::test]
    async fn immutable_files_survive_new_instances_and_failed_publication() {
        let root = root();
        let store = FileStore::new(root.clone(), "asia".into(), HashMap::new(), true);
        let id = uuid::Uuid::new_v4().to_string();
        assert!(
            store
                .put(&id, &Bytes::from_static(b"not published"))
                .await
                .is_err()
        );
        store.prepare().await.unwrap();
        let first = Bytes::from_static(b"image one");
        let second = Bytes::from_static(b"image two");
        let (a, b) = tokio::join!(store.put(&id, &first), store.put(&id, &second));
        assert_ne!(a.is_ok(), b.is_ok());
        let (key, want) = if let Ok(key) = a {
            (key, b"image one".as_slice())
        } else {
            (b.unwrap(), b"image two".as_slice())
        };
        assert_eq!(store.get(&key).await.unwrap(), want);
        assert!(
            store
                .put(&id, &Bytes::from_static(b"overwritten"))
                .await
                .is_err()
        );
        assert_eq!(store.get(&key).await.unwrap(), want);
        let reader = FileStore::new(root.clone(), "asia".into(), HashMap::new(), false);
        reader.prepare().await.unwrap();
        assert_eq!(reader.get(&key).await.unwrap(), want);
        assert!(
            reader
                .put(
                    &uuid::Uuid::new_v4().to_string(),
                    &Bytes::from_static(b"disabled")
                )
                .await
                .is_err()
        );
        for bad in [
            "../../escape",
            "ASDF",
            "00000000000000000000000000000000",
            "fs:asia:../../escape",
        ] {
            assert!(
                store
                    .put(bad, &Bytes::from_static(b"invalid"))
                    .await
                    .is_err()
            );
            assert!(store.get(bad).await.is_err());
        }
        assert!(matches!(
            store
                .get(&format!("fs:asia:{}", uuid::Uuid::new_v4()))
                .await,
            Err(sqlx::Error::RowNotFound)
        ));
        assert!(
            store
                .put(&uuid::Uuid::new_v4().to_string(), &Bytes::new())
                .await
                .is_err()
        );
        for entry in fs::read_dir(root.join(&id[..2])).unwrap() {
            assert_eq!(entry.unwrap().file_name().to_str().unwrap(), id);
        }
        fs::remove_dir_all(root).unwrap();
    }

    async fn serve_file(
        State(store): State<FileStore>,
        AxumPath((bucket, id)): AxumPath<(String, String)>,
    ) -> axum::response::Response {
        if !valid_id(&id) || bucket != id[..2] {
            return StatusCode::NOT_FOUND.into_response();
        }
        match store.get(&format!("fs:europe:{id}")).await {
            Ok(data) => data.into_response(),
            Err(_) => StatusCode::NOT_FOUND.into_response(),
        }
    }

    #[tokio::test]
    async fn regional_file_reads_preserve_bytes_and_missing_file_errors() {
        let root = root();
        let europe = FileStore::new(root.clone(), "europe".into(), HashMap::new(), true);
        europe.prepare().await.unwrap();
        let payload = Bytes::from_static(include_bytes!("../tests/fixtures/small.jpg"));
        let key = europe
            .put(&uuid::Uuid::new_v4().to_string(), &payload)
            .await
            .unwrap();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let app = Router::new()
            .route("/internal/astra/files/{bucket}/{id}", get(serve_file))
            .with_state(europe);
        let server = tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        let asia = FileStore::new(
            std::env::temp_dir(),
            "asia".into(),
            [("europe".into(), address.to_string())].into(),
            false,
        );
        assert_eq!(asia.get(&key).await.unwrap(), payload);
        assert_eq!(asia.get(&key).await.unwrap(), payload);
        assert!(matches!(
            asia.get(&format!("fs:europe:{}", uuid::Uuid::new_v4()))
                .await,
            Err(sqlx::Error::RowNotFound)
        ));
        assert!(asia.get("fs:unknown:../../escape").await.is_err());
        assert!(
            asia.get(&format!("fs:america:{}", uuid::Uuid::new_v4()))
                .await
                .is_err()
        );
        server.abort();
        fs::remove_dir_all(root).unwrap();
    }
}
