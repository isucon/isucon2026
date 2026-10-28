//! Persist a valid post image near its uploader, then forward its signed reference.
use crate::{image_store::ImageStore, stored_upload::StoredUpload};
use axum::http::{HeaderMap, header};
use bytes::{Bytes, BytesMut};
use chrono::Utc;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::convert::Infallible;

#[derive(Clone)]
pub struct RegionalUpload {
    auth_read_db: PgPool,
    store: ImageStore,
    region: String,
    pub(crate) key: [u8; 32],
}
pub struct PreparedUpload {
    pub body: Bytes,
    pub content_type: String,
    pub image: Bytes,
    pub reference: StoredUpload,
}
impl RegionalUpload {
    pub fn new(auth_read_db: PgPool, store: ImageStore, region: String, key: [u8; 32]) -> Self {
        assert!(
            store.writes_enabled
                && matches!(region.as_str(), "europe" | "america")
                && store.has_region(&region)
        );
        Self {
            auth_read_db,
            store,
            region,
            key,
        }
    }
    pub async fn prepare(
        &self,
        headers: &HeaderMap,
        client_ip: &str,
        content_type: &str,
        body: Bytes,
    ) -> Result<Option<PreparedUpload>, anyhow::Error> {
        let Some(token) = headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.strip_prefix("Bearer "))
        else {
            return Ok(None);
        };
        // Use the regional read DB, whose visibility is protected by the
        // normal write-response fences. This prevents unauthenticated/banned
        // clients from filling storage without another cross-region query.
        // The authoritative writer rechecks all permissions before publication.
        let allowed: bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM login_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.active AND u.status='active' AND u.region=$2) AND NOT EXISTS(SELECT 1 FROM ip_bans WHERE ip=$3 AND banned_until>clock_timestamp())")
            .bind(hex::encode(Sha256::digest(token.as_bytes()))).bind(&self.region).bind(client_ip)
            .fetch_one(&self.auth_read_db).await?;
        if !allowed {
            return Ok(None);
        }
        let Ok(boundary) = multer::parse_boundary(content_type) else {
            return Ok(None);
        };
        let mut multipart = multer::Multipart::new(
            futures_util::stream::once(async { Ok::<_, Infallible>(body) }),
            boundary,
        );
        let mut fields = Vec::new();
        let mut image = None;
        loop {
            let field = match multipart.next_field().await {
                Ok(Some(field)) => field,
                Ok(None) => break,
                Err(_) => return Ok(None),
            };
            let selected = image.is_none()
                && field.name() == Some("image")
                && field.file_name().is_some_and(|v| !v.is_empty());
            let headers = field.headers().clone();
            let Ok(data) = field.bytes().await else {
                return Ok(None);
            };
            if selected {
                image = Some(data.clone());
            }
            fields.push((headers, if selected { Bytes::new() } else { data }));
        }
        let Some(image) = image else {
            return Ok(None);
        };
        let Ok(dimensions) = crate::jpeg::decode(image.clone(), 4096).await else {
            return Ok(None);
        };
        let boundary = loop {
            let boundary = format!("astra{}", uuid::Uuid::new_v4().simple());
            if !fields.iter().any(|(_, data)| {
                data.windows(boundary.len())
                    .any(|bytes| bytes == boundary.as_bytes())
            }) {
                break boundary;
            }
        };
        let mut forwarded = BytesMut::new();
        for (headers, data) in fields {
            forwarded.extend_from_slice(format!("--{boundary}\r\n").as_bytes());
            for (name, value) in &headers {
                forwarded.extend_from_slice(name.as_str().as_bytes());
                forwarded.extend_from_slice(b": ");
                forwarded.extend_from_slice(value.as_bytes());
                forwarded.extend_from_slice(b"\r\n");
            }
            forwarded.extend_from_slice(b"\r\n");
            forwarded.extend_from_slice(&data);
            forwarded.extend_from_slice(b"\r\n");
        }
        forwarded.extend_from_slice(format!("--{boundary}--\r\n").as_bytes());
        let key = self
            .store
            .put_regional(&self.region, &uuid::Uuid::new_v4().to_string(), &image)
            .await?;
        let reference = StoredUpload {
            asset_key: key,
            width: dimensions.width,
            height: dimensions.height,
            bytes: image.len(),
            expires: Utc::now().timestamp() + 60,
        };
        Ok(Some(PreparedUpload {
            body: forwarded.freeze(),
            content_type: format!("multipart/form-data; boundary={boundary}"),
            image,
            reference,
        }))
    }
}
