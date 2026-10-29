use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::{Arc, LazyLock, Mutex, Weak};

use axum::extract::{RawQuery, State};
use axum::http::{HeaderValue, StatusCode, header};
use axum::response::{IntoResponse, Response};
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use bytes::Bytes;
use chrono::{DateTime, Datelike, Utc};
use serde::Deserialize;

use crate::api_error::{Error, new_api_error};
use crate::bind::{Bind, Bindable, Binder, parse_query};
use crate::image_signature::{image_signing_key, verify_image_signature};
use crate::server::HandlerContext;

#[derive(Default, Deserialize)]
#[serde(default)]
pub struct GetImageRequest {
    #[serde(skip)]
    pub image_id: String,
    #[serde(skip)]
    pub exp: String,
    #[serde(skip)]
    pub sig: String,
}

impl Bindable for GetImageRequest {
    fn bind(&mut self, b: &Binder) -> Result<(), String> {
        b.param("imageId", &mut self.image_id)?;
        b.query("exp", &mut self.exp)?;
        b.query("sig", &mut self.sig)
    }
}

#[derive(Clone)]
struct CachedImage {
    owner_id: Option<String>,
    version: String,
    created_at: DateTime<Utc>,
    data: Bytes,
    recently_used: bool,
}

struct ImageCache {
    images: HashMap<String, CachedImage>,
    owners: HashMap<String, HashSet<String>>,
    order: VecDeque<String>,
    bytes: usize,
    capacity: usize,
}

impl Default for ImageCache {
    fn default() -> Self {
        Self {
            images: HashMap::new(),
            owners: HashMap::new(),
            order: VecDeque::new(),
            bytes: 0,
            capacity: 1024 * 1024 * 1024,
        }
    }
}

#[derive(Default)]
struct ImageHints {
    timestamps: HashMap<String, DateTime<Utc>>,
    order: VecDeque<String>,
}

static IMAGE_HINTS: LazyLock<Mutex<ImageHints>> = LazyLock::new(Mutex::default);
const IMAGE_HINT_ENTRIES: usize = 32768;

/// A post timestamp is only a lookup hint, never proof of visibility or content.
/// Importing or moving an image may invalidate it; the handler then searches all
/// partitions. Keep hints separately so issuing a URL does not cache image data.
pub fn remember_image_timestamp(image_id: &str, timestamp: DateTime<Utc>) {
    let mut hints = IMAGE_HINTS.lock().unwrap();
    if let Some(current) = hints.timestamps.get_mut(image_id) {
        *current = timestamp;
        return;
    }
    if hints.timestamps.len() >= IMAGE_HINT_ENTRIES
        && let Some(oldest) = hints.order.pop_front()
    {
        hints.timestamps.remove(&oldest);
    }
    hints.order.push_back(image_id.to_owned());
    hints.timestamps.insert(image_id.to_owned(), timestamp);
}

static IMAGE_CACHE: LazyLock<Mutex<ImageCache>> = LazyLock::new(|| {
    let mib: usize = std::env::var("ISUREAL_IMAGE_CACHE_MIB")
        .unwrap_or_else(|_| "1024".into())
        .parse()
        .expect("image cache capacity must be an integer MiB value");
    assert!(
        (64..=1024).contains(&mib),
        "image cache must be 64–1024 MiB"
    );
    Mutex::new(ImageCache {
        capacity: mib * 1024 * 1024,
        ..ImageCache::default()
    })
});
const IMAGE_CACHE_ENTRIES: usize = 16384;

pub(crate) fn initialize_image_cache() {
    LazyLock::force(&IMAGE_CACHE);
}

// Keep only weak references here: completed reads retain no image bytes or
// permanent locks. Cap retained keys even when many distinct images are read.
type ImageRead = Arc<tokio::sync::Mutex<()>>;
static IMAGE_READS: LazyLock<Mutex<HashMap<String, Weak<tokio::sync::Mutex<()>>>>> =
    LazyLock::new(Mutex::default);
fn image_read(id: &str) -> ImageRead {
    let mut reads = IMAGE_READS.lock().unwrap();
    if let Some(read) = reads.get(id).and_then(Weak::upgrade) {
        return read;
    }
    if reads.len() >= IMAGE_CACHE_ENTRIES {
        reads.retain(|_, read| read.strong_count() > 0);
    }
    let read = Arc::new(tokio::sync::Mutex::new(()));
    if reads.len() < IMAGE_CACHE_ENTRIES {
        reads.insert(id.to_owned(), Arc::downgrade(&read));
    }
    read
}

fn cached_bytes_for_object(id: &str, object: &ImageObject) -> Option<Bytes> {
    let mut cache = IMAGE_CACHE.lock().unwrap();
    let image = cache.images.get_mut(id)?;
    let matches = (image.created_at == object.created_at && image.version == object.version)
        || object
            .asset_key
            .as_ref()
            .is_some_and(|key| image.version.strip_prefix("asset:") == Some(key));
    if !matches {
        return None;
    }
    image.recently_used = true;
    let data = image.data.clone();
    let version = image.version.clone();
    cache.update_metadata(id, &version, object.created_at, &object.owner_id);
    Some(data)
}

pub fn remember_stored_image(id: String, created_at: DateTime<Utc>, data: Bytes) {
    remember_asset_image(id.clone(), &id, created_at, data);
}

pub fn remember_asset_image(id: String, asset_key: &str, created_at: DateTime<Utc>, data: Bytes) {
    // An asset key names bytes that the application role cannot update/delete.
    // Visibility, owner credentials and the current metadata reference are
    // always checked again by IMAGE_QUERY before using these bytes.
    let version = format!("asset:{asset_key}");
    IMAGE_CACHE.lock().unwrap().insert(
        id,
        CachedImage {
            owner_id: None,
            version,
            created_at,
            data,
            recently_used: false,
        },
    );
}

/// Pin only this owner's bytes while the export query checks their current
/// metadata versions. Permissions and post visibility remain in fresh SQL.
pub(crate) struct ExportCache {
    pub ids: Vec<String>,
    pub versions: Vec<String>,
    pub timestamps: Vec<DateTime<Utc>>,
    pub bytes: HashMap<String, Bytes>,
}

pub(crate) fn cached_export_images(owner: &str) -> ExportCache {
    IMAGE_CACHE.lock().unwrap().export(owner)
}

impl ImageCache {
    fn export(&self, owner: &str) -> ExportCache {
        let mut result = ExportCache {
            ids: Vec::new(),
            versions: Vec::new(),
            timestamps: Vec::new(),
            bytes: HashMap::new(),
        };
        if let Some(ids) = self.owners.get(owner) {
            for id in ids {
                let image = &self.images[id];
                result.ids.push(id.clone());
                result.versions.push(image.version.clone());
                result.timestamps.push(image.created_at);
                result.bytes.insert(id.clone(), image.data.clone());
            }
        }
        result
    }

    fn remove_owner(&mut self, owner: Option<&str>, id: &str) {
        if let Some(owner) = owner
            && let Some(ids) = self.owners.get_mut(owner)
        {
            ids.remove(id);
            if ids.is_empty() {
                self.owners.remove(owner);
            }
        }
    }

    fn update_metadata(&mut self, id: &str, version: &str, timestamp: DateTime<Utc>, owner: &str) {
        let Some(image) = self.images.get_mut(id) else {
            return;
        };
        if image.version != version {
            return;
        }
        image.created_at = timestamp;
        if image.owner_id.as_deref() == Some(owner) {
            return;
        }
        let old_owner = image.owner_id.replace(owner.to_owned());
        self.remove_owner(old_owner.as_deref(), id);
        self.owners
            .entry(owner.to_owned())
            .or_default()
            .insert(id.to_owned());
    }

    fn get(&mut self, id: &str) -> Option<CachedImage> {
        let image = self.images.get_mut(id)?;
        image.recently_used = true;
        Some(image.clone())
    }

    // CLOCK gives recently accessed bytes a second chance without maintaining
    // a linked list or moving an entry on every request. Each pass clears its
    // access bit, so eviction always finishes even if every image was used.
    fn evict(&mut self) -> bool {
        while let Some(key) = self.order.pop_front() {
            if let Some(image) = self.images.get_mut(&key)
                && image.recently_used
            {
                image.recently_used = false;
                self.order.push_back(key);
                continue;
            }
            if let Some(old) = self.images.remove(&key) {
                self.bytes -= old.data.len();
                self.remove_owner(old.owner_id.as_deref(), &key);
                return true;
            }
        }
        false
    }

    fn insert(&mut self, id: String, image: CachedImage) {
        if image.data.len() > self.capacity {
            return;
        }
        if let Some(old) = self.images.remove(&id) {
            self.bytes -= old.data.len();
            self.remove_owner(old.owner_id.as_deref(), &id);
            self.order.retain(|key| key != &id);
        }
        while self.bytes + image.data.len() > self.capacity
            || self.images.len() >= IMAGE_CACHE_ENTRIES
        {
            if !self.evict() {
                break;
            }
        }
        if let Some(owner) = &image.owner_id {
            self.owners
                .entry(owner.clone())
                .or_default()
                .insert(id.clone());
        }
        self.bytes += image.data.len();
        self.order.push_back(id.clone());
        self.images.insert(id, image);
    }
}

pub async fn get_image_handler(
    State(h): State<Arc<HandlerContext>>,
    RawQuery(raw_query): RawQuery,
    input: Result<Bind<GetImageRequest>, Error>,
) -> Response {
    let mut response = get_image(&h, raw_query.unwrap_or_default(), input)
        .await
        .into_response();
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response
}

async fn get_image(
    h: &HandlerContext,
    raw_query: String,
    input: Result<Bind<GetImageRequest>, Error>,
) -> Result<Response, Error> {
    let Ok(Bind(input)) = input else {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    };
    let query = parse_query(&raw_query);
    if !query.is_ok_and(|query| {
        query.get("exp").map(Vec::len) == Some(1) && query.get("sig").map(Vec::len) == Some(1)
    }) {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    }
    let exp = match input.exp.parse::<i64>() {
        Ok(exp) if exp.to_string() == input.exp && exp > Utc::now().timestamp() => exp,
        _ => return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden")),
    };
    let sig = match URL_SAFE_NO_PAD.decode(&input.sig) {
        Ok(sig) if sig.len() == 32 && URL_SAFE_NO_PAD.encode(&sig) == input.sig => sig,
        _ => return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden")),
    };

    let cached = IMAGE_CACHE.lock().unwrap().get(&input.image_id);
    // Always check current visibility and owner credentials. Only image bytes
    // are cached; xmin changes when a row is replaced or modified, including
    // initialization, and prevents returning stale data under the same ID.
    // The cached timestamp is a search hint, never an authorization result.
    // Check its partition first; a moved/replaced row falls back to all months.
    let timestamp = cached.as_ref().map(|image| image.created_at).or_else(|| {
        IMAGE_HINTS
            .lock()
            .unwrap()
            .timestamps
            .get(&input.image_id)
            .copied()
    });
    let mut object = if let Some(timestamp) = timestamp {
        load_image_object(
            &h.db,
            &input.image_id,
            cached.as_ref(),
            Some(timestamp),
            partition_image_query(timestamp),
        )
        .await
        .or_else(|error| match &error {
            // A deployment/import may use different partition names. The
            // parent lookup below still enforces all current authorization.
            sqlx::Error::Database(db) if db.code().as_deref() == Some("42P01") => Ok(None),
            _ => Err(error),
        })?
    } else {
        None
    };
    if object.is_none() {
        object = load_image_object(
            &h.db,
            &input.image_id,
            cached.as_ref(),
            timestamp,
            IMAGE_QUERY,
        )
        .await?;
    }
    let Some(object) = object else {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    };
    let key = image_signing_key(&object.secret_key_hash)?;
    if !verify_image_signature(&key, &input.image_id, exp, &sig) {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    }
    if exp <= Utc::now().timestamp() {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    }
    let mut object = object;
    let data = match object.data.take() {
        Some(data) => {
            // Authorization above stays fresh for every request. Only the
            // immutable/current-version byte load is shared among waiters.
            let read = image_read(&input.image_id);
            let _reading = read.lock().await;
            if let Some(bytes) = cached_bytes_for_object(&input.image_id, &object) {
                bytes
            } else {
                let data = if let Some(key) = &object.asset_key {
                    h.image_store
                        .as_ref()
                        .ok_or_else(|| anyhow::anyhow!("image store unavailable"))?
                        .get(key)
                        .await?
                } else {
                    data
                };
                let data = Bytes::from(data);
                IMAGE_CACHE.lock().unwrap().insert(
                    input.image_id,
                    CachedImage {
                        owner_id: Some(object.owner_id),
                        data: data.clone(),
                        recently_used: false,
                        version: object.version,
                        created_at: object.created_at,
                    },
                );
                data
            }
        }
        None => {
            let cached = cached.expect("data omitted only for the matching cached version");
            // Immutable asset identity validates bytes even when the upload
            // response timestamp differs from the image row timestamp.
            if cached.created_at != object.created_at
                || cached.owner_id.as_deref() != Some(object.owner_id.as_str())
            {
                IMAGE_CACHE.lock().unwrap().update_metadata(
                    &input.image_id,
                    &cached.version,
                    object.created_at,
                    &object.owner_id,
                );
            }
            cached.data
        }
    };
    Ok((
        StatusCode::OK,
        [(header::CONTENT_TYPE, object.media_type)],
        data,
    )
        .into_response())
}

#[derive(sqlx::FromRow)]
struct ImageObject {
    owner_id: String,
    data: Option<Vec<u8>>,
    media_type: String,
    secret_key_hash: String,
    version: String,
    created_at: DateTime<Utc>,
    asset_key: Option<String>,
}

// Keep the owner lookup correlated: a generic parent-table plan estimates
// one image per partition and otherwise chooses a full users scan per GET.
// OFFSET 0 preserves every matching row while preventing that join flattening.
const IMAGE_QUERY: &str = r#"
   SELECT CASE WHEN (i.created_at = $3 AND i.xmin::text = $2) OR (i.asset_key IS NOT NULL AND 'asset:' || i.asset_key = $2) THEN NULL ELSE i.data END AS data,
          u.id AS owner_id, i.media_type, u.secret_key_hash, i.xmin::text AS version, i.created_at, i.asset_key
   FROM image_objects i
   JOIN LATERAL (
     SELECT id, secret_key_hash, profile_image_id FROM users
     WHERE id = i.owner_user_id AND status = 'active' OFFSET 0
   ) u ON true
   WHERE i.id = $1 AND (
     u.profile_image_id = i.id
     OR EXISTS (
       SELECT 1 FROM posts p
       WHERE p.image_id = i.id AND p.author_id = u.id AND p.deleted_at IS NULL
     )
     OR EXISTS (
       SELECT 1 FROM realmojis r
       JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
       JOIN users author ON author.id = p.author_id AND author.status = 'active'
       WHERE r.image_id = i.id AND r.reactor_id = u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
     )
   )
  "#;

// Match the monthly partitions in 001-schema.sql. All SQL identifiers come
// from this fixed range, never request data. A missing/moved row falls back to
// the parent table; the timestamp is only a performance hint.
const FIRST_IMAGE_MONTH: i32 = 2024 * 12 + 8;
const IMAGE_MONTHS: usize = 34;
// October 2026 is split at 2026-10-28 06:00 UTC; later October rows live in
// image_objects_20261028 (unix seconds, inclusive start and exclusive end).
const SPLIT_IMAGE_PARTITION: (i64, i64) = (1_793_167_200, 1_793_491_200);
static PARTITION_IMAGE_QUERIES: LazyLock<Vec<String>> = LazyLock::new(|| {
    (0..=IMAGE_MONTHS + 1)
        .map(|offset| {
            let table = if offset == IMAGE_MONTHS + 1 {
                "image_objects_20261028".to_owned()
            } else if offset == IMAGE_MONTHS {
                "image_objects_default".to_owned()
            } else {
                let month = FIRST_IMAGE_MONTH + offset as i32;
                format!("image_objects_{:04}{:02}", month / 12, month % 12 + 1)
            };
            format!(
                "{} AND i.created_at = $3",
                IMAGE_QUERY.replace("FROM image_objects i", &format!("FROM {table} i"))
            )
        })
        .collect()
});

fn partition_image_query(timestamp: DateTime<Utc>) -> &'static str {
    let seconds = timestamp.timestamp();
    if (SPLIT_IMAGE_PARTITION.0..SPLIT_IMAGE_PARTITION.1).contains(&seconds) {
        return &PARTITION_IMAGE_QUERIES[IMAGE_MONTHS + 1];
    }
    let offset = timestamp.year() * 12 + timestamp.month0() as i32 - FIRST_IMAGE_MONTH;
    let index = usize::try_from(offset)
        .ok()
        .filter(|offset| *offset < IMAGE_MONTHS)
        .unwrap_or(IMAGE_MONTHS);
    &PARTITION_IMAGE_QUERIES[index]
}

async fn load_image_object(
    db: &sqlx::PgPool,
    image_id: &str,
    cached: Option<&CachedImage>,
    timestamp: Option<DateTime<Utc>>,
    query: &str,
) -> Result<Option<ImageObject>, sqlx::Error> {
    sqlx::query_as(query)
        .bind(image_id)
        .bind(cached.map(|image| image.version.as_str()))
        .bind(timestamp)
        .fetch_optional(db)
        .await
}

#[cfg(test)]
mod cache_tests {
    use super::*;

    fn entry(bytes: &'static [u8]) -> CachedImage {
        CachedImage {
            owner_id: None,
            version: "1".into(),
            created_at: Utc::now(),
            data: Bytes::from_static(bytes),
            recently_used: false,
        }
    }

    #[test]
    fn eviction_retains_recently_used_images_and_preserves_byte_accounting() {
        let mut cache = ImageCache::default();
        cache.insert("a".into(), entry(b"aa"));
        cache.insert("b".into(), entry(b"bbb"));
        cache.insert("c".into(), entry(b"c"));
        assert_eq!(cache.get("a").unwrap().data.as_ref(), b"aa");
        assert!(cache.evict());
        assert!(!cache.images.contains_key("b"));
        assert!(cache.images.contains_key("a"));
        assert_eq!(cache.bytes, 3);
        cache.get("a");
        cache.get("c");
        assert!(cache.evict()); // A full pass must still finish.
        assert_eq!(cache.bytes, 2);
        cache.insert("a".into(), entry(b"replacement"));
        assert_eq!(cache.bytes, 11);
        assert_eq!(cache.order.len(), 1);
        assert!(cache.evict());
        assert_eq!(cache.bytes, 0);
        assert!(!cache.evict());
    }

    #[test]
    fn byte_budget_evicts_cold_entries_and_retained_responses_survive() {
        let mut cache = ImageCache {
            capacity: 6,
            ..ImageCache::default()
        };
        cache.insert("a".into(), entry(b"aaa"));
        cache.insert("b".into(), entry(b"bbb"));
        let response = cache.get("a").unwrap().data;
        cache.insert("c".into(), entry(b"ccc"));
        assert!(cache.get("b").is_none());
        assert_eq!(cache.bytes, 6);
        cache.insert("too-large".into(), entry(b"1234567"));
        assert_eq!(cache.bytes, 6);
        cache.insert("a".into(), entry(b"aaaa"));
        assert_eq!(cache.bytes, 4);
        assert_eq!(response.as_ref(), b"aaa");
    }
    #[test]
    fn owner_index_tracks_learning_replacement_and_eviction() {
        let mut cache = ImageCache {
            capacity: 6,
            ..ImageCache::default()
        };
        cache.insert("a".into(), entry(b"aa"));
        assert!(cache.export("alice").ids.is_empty());
        let time = Utc::now();
        cache.update_metadata("a", "wrong-version", time, "alice");
        assert!(cache.owners.is_empty());
        cache.update_metadata("a", "1", time, "alice");
        let pinned = cache.export("alice");
        assert_eq!(pinned.ids, ["a"]);
        assert_eq!(pinned.timestamps, [time]);
        assert_eq!(pinned.versions, ["1"]);
        cache.update_metadata("a", "1", time, "bob");
        assert!(!cache.owners.contains_key("alice"));
        assert_eq!(cache.export("bob").ids, ["a"]);
        let mut b = entry(b"bb");
        b.owner_id = Some("alice".into());
        cache.insert("b".into(), b);
        cache.insert("a".into(), entry(b"new"));
        assert!(!cache.owners.contains_key("bob"));
        assert_eq!(cache.export("alice").ids, ["b"]);
        cache.insert("c".into(), entry(b"cc")); // Evicts b to fit the byte budget.
        assert!(cache.owners.is_empty());
        assert!(cache.export("alice").ids.is_empty());
        assert_eq!(pinned.bytes["a"].as_ref(), b"aa");
        cache.update_metadata("missing", "1", time, "alice");
        assert!(cache.owners.is_empty());
    }

    #[test]
    #[ignore = "local export-cache timing comparison"]
    fn compare_export_owner_index_with_full_scan() {
        use std::{hint::black_box, time::Instant};
        let mut cache = ImageCache::default();
        for i in 0..IMAGE_CACHE_ENTRIES {
            let mut image = entry(b"sample image");
            image.owner_id = Some(format!("owner-{}", i % 1024));
            cache.insert(format!("image-{i}"), image);
        }
        let scan = |owner: &str| {
            let mut result = ExportCache {
                ids: Vec::new(),
                versions: Vec::new(),
                timestamps: Vec::new(),
                bytes: HashMap::new(),
            };
            for (id, image) in &cache.images {
                if image.owner_id.as_deref() == Some(owner) {
                    result.ids.push(id.clone());
                    result.versions.push(image.version.clone());
                    result.timestamps.push(image.created_at);
                    result.bytes.insert(id.clone(), image.data.clone());
                }
            }
            result
        };
        for owner in ["owner-0", "owner-31", "missing"] {
            assert_eq!(scan(owner).bytes, cache.export(owner).bytes);
        }
        for pass in 0..3 {
            let started = Instant::now();
            for i in 0..2000 {
                black_box(scan(black_box(&format!("owner-{}", i % 1024))));
            }
            let scan_us = started.elapsed().as_micros();
            let started = Instant::now();
            for i in 0..2000 {
                black_box(cache.export(black_box(&format!("owner-{}", i % 1024))));
            }
            println!(
                "pass={pass} exports=2000 entries={} scan_us={scan_us} indexed_us={}",
                IMAGE_CACHE_ENTRIES,
                started.elapsed().as_micros()
            );
        }
    }
}
