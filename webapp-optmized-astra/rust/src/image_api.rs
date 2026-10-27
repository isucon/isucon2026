use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, LazyLock, Mutex};

use axum::extract::{RawQuery, State};
use axum::http::{HeaderValue, StatusCode, header};
use axum::response::{IntoResponse, Response};
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use bytes::Bytes;
use chrono::{DateTime, Datelike, Utc};
use serde::Deserialize;
use subtle::ConstantTimeEq;

use crate::api_error::{Error, new_api_error};
use crate::bind::{Bind, Bindable, Binder, parse_query};
use crate::image_signature::{image_signature, image_signing_key};
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
    version: String,
    created_at: DateTime<Utc>,
    data: Bytes,
    recently_used: bool,
}

#[derive(Default)]
struct ImageCache {
    images: HashMap<String, CachedImage>,
    order: VecDeque<String>,
    bytes: usize,
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

static IMAGE_CACHE: LazyLock<Mutex<ImageCache>> = LazyLock::new(Mutex::default);
const IMAGE_CACHE_BYTES: usize = 1024 * 1024 * 1024;
const IMAGE_CACHE_ENTRIES: usize = 16384;

pub fn remember_stored_image(id: String, created_at: DateTime<Utc>, data: Bytes) {
    // An asset key names bytes that the application role cannot update/delete.
    // Visibility, owner credentials and the current metadata reference are
    // always checked again by IMAGE_QUERY before using these bytes.
    let version = format!("asset:{id}");
    IMAGE_CACHE.lock().unwrap().insert(
        id,
        CachedImage {
            version,
            created_at,
            data,
            recently_used: false,
        },
    );
}

impl ImageCache {
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
                return true;
            }
        }
        false
    }

    fn insert(&mut self, id: String, image: CachedImage) {
        if image.data.len() > IMAGE_CACHE_BYTES {
            return;
        }
        if let Some(old) = self.images.remove(&id) {
            self.bytes -= old.data.len();
            self.order.retain(|key| key != &id);
        }
        while self.bytes + image.data.len() > IMAGE_CACHE_BYTES
            || self.images.len() >= IMAGE_CACHE_ENTRIES
        {
            if !self.evict() {
                break;
            }
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
    if !bool::from(sig.ct_eq(&image_signature(&key, &input.image_id, exp))) {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    }
    if exp <= Utc::now().timestamp() {
        return Err(new_api_error(StatusCode::FORBIDDEN, "image_forbidden"));
    }
    let data = match object.data {
        Some(data) => {
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
                    data: data.clone(),
                    recently_used: false,
                    version: object.version,
                    created_at: object.created_at,
                },
            );
            data
        }
        None => {
            let cached = cached.expect("data omitted only for the matching cached version");
            // Immutable asset identity validates bytes even when the upload
            // response timestamp differs from the image row timestamp.
            if cached.created_at != object.created_at
                && let Some(current) = IMAGE_CACHE.lock().unwrap().images.get_mut(&input.image_id)
                && current.version == cached.version
            {
                current.created_at = object.created_at;
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
          i.media_type, u.secret_key_hash, i.xmin::text AS version, i.created_at, i.asset_key
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
static PARTITION_IMAGE_QUERIES: LazyLock<Vec<String>> = LazyLock::new(|| {
    (0..=IMAGE_MONTHS)
        .map(|offset| {
            let table = if offset == IMAGE_MONTHS {
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
}
