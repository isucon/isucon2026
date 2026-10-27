use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, LazyLock, Mutex};

use axum::extract::{RawQuery, State};
use axum::http::{HeaderValue, StatusCode, header};
use axum::response::{IntoResponse, Response};
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use bytes::Bytes;
use chrono::{DateTime, Utc};
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
const IMAGE_CACHE_BYTES: usize = 512 * 1024 * 1024;
const IMAGE_CACHE_ENTRIES: usize = 4096;

impl ImageCache {
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
            let Some(key) = self.order.pop_front() else {
                break;
            };
            if let Some(old) = self.images.remove(&key) {
                self.bytes -= old.data.len();
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

    let cached = IMAGE_CACHE
        .lock()
        .unwrap()
        .images
        .get(&input.image_id)
        .cloned();
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
    let mut object = if timestamp.is_some() {
        load_image_object(
            &h.db,
            &input.image_id,
            cached.as_ref(),
            timestamp,
            &CACHED_IMAGE_QUERY,
        )
        .await?
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
                    version: object.version,
                    created_at: object.created_at,
                },
            );
            data
        }
        None => {
            cached
                .expect("data omitted only for the matching cached version")
                .data
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

const IMAGE_QUERY: &str = r#"
   SELECT CASE WHEN i.xmin::text = $2 AND i.created_at = $3 THEN NULL ELSE i.data END AS data,
          i.media_type, u.secret_key_hash, i.xmin::text AS version, i.created_at, i.asset_key
   FROM image_objects i JOIN users u ON u.id = i.owner_user_id AND u.status = 'active'
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

static CACHED_IMAGE_QUERY: LazyLock<String> =
    LazyLock::new(|| format!("{IMAGE_QUERY} AND i.created_at = $3"));

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
