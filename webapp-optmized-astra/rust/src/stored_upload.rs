//! Authenticated, immutable regional image handoff to the existing writer.
//! A proof never substitutes for the writer's session or publication checks.
use axum::{
    extract::{ConnectInfo, Request, State},
    http::{HeaderMap, Method, StatusCode},
    middleware::Next,
    response::{IntoResponse, Response},
};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use bytes::Bytes;
use chrono::Utc;
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    net::{IpAddr, SocketAddr},
};

pub const HEADER: &str = "x-astra-stored-upload";

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StoredUpload {
    pub asset_key: String,
    pub width: u16,
    pub height: u16,
    pub bytes: usize,
    pub expires: i64,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct SignedUpload {
    image: StoredUpload,
    signature: String,
}

pub(crate) fn key_from_env() -> Option<[u8; 32]> {
    let enabled = std::env::var("ISUREAL_REGIONAL_IMAGE_WRITES").unwrap_or_else(|_| "0".into());
    assert!(matches!(enabled.as_str(), "0" | "1"));
    if enabled == "0" {
        return None;
    }
    let value = std::env::var("ISUREAL_UPLOAD_HANDOFF_KEY")
        .expect("private upload handoff key is configured");
    let decoded = hex::decode(value).expect("valid upload handoff key encoding");
    Some(decoded.try_into().expect("32-byte upload handoff key"))
}

fn mac(
    key: &[u8; 32],
    image: &StoredUpload,
    uri: &str,
    headers: &HeaderMap,
    body: &Bytes,
) -> Hmac<Sha256> {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).unwrap();
    let mut field = |value: &[u8]| {
        mac.update(&(value.len() as u64).to_be_bytes());
        mac.update(value);
    };
    field(b"astra-regional-post-v1");
    field(&serde_json::to_vec(image).unwrap());
    field(uri.as_bytes());
    for header in ["authorization", "content-type", "x-client-ip"] {
        field(
            headers
                .get(header)
                .map(|v| v.as_bytes())
                .unwrap_or_default(),
        );
    }
    field(&Sha256::digest(body));
    mac
}

pub fn sign(
    key: &[u8; 32],
    image: StoredUpload,
    uri: &str,
    headers: &HeaderMap,
    body: &Bytes,
) -> String {
    let signature = hex::encode(mac(key, &image, uri, headers, body).finalize().into_bytes());
    URL_SAFE_NO_PAD.encode(serde_json::to_vec(&SignedUpload { image, signature }).unwrap())
}

#[derive(Clone)]
pub struct UploadAuthenticator {
    key: [u8; 32],
    peers: HashMap<IpAddr, String>,
}
impl UploadAuthenticator {
    pub fn new(key: [u8; 32], peers: HashMap<IpAddr, String>) -> Self {
        Self { key, peers }
    }
    pub(crate) fn from_env() -> Option<Self> {
        let peers = std::env::var("ISUREAL_WRITE_INGRESS_JSON").unwrap_or_default();
        if peers.is_empty() {
            return None;
        }
        let key = key_from_env()?;
        Some(Self::new(
            key,
            serde_json::from_str(&peers).expect("known writer peers"),
        ))
    }
    fn valid(
        &self,
        region: &str,
        signed: &SignedUpload,
        uri: &str,
        headers: &HeaderMap,
        body: &Bytes,
    ) -> bool {
        let image = &signed.image;
        let now = Utc::now().timestamp();
        if image.expires < now
            || image.expires > now + 120
            || image.width == 0
            || image.height == 0
            || image.width > 4096
            || image.height > 4096
            || image.bytes == 0
            || image.bytes > 32 * 1024 * 1024
        {
            return false;
        }
        let Some((stored_region, id)) = image.asset_key.split_once(':') else {
            return false;
        };
        if stored_region != region
            || !matches!(region, "europe" | "america")
            || uuid::Uuid::parse_str(id).is_err()
        {
            return false;
        }
        let Ok(signature) = hex::decode(&signed.signature) else {
            return false;
        };
        mac(&self.key, image, uri, headers, body)
            .verify_slice(&signature)
            .is_ok()
    }
}

pub async fn authenticate(
    State(auth): State<UploadAuthenticator>,
    mut request: Request,
    next: Next,
) -> Response {
    if request.method() != Method::POST || request.uri().path() != "/api/posts" {
        return next.run(request).await;
    }
    let region = request
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .and_then(|ConnectInfo(peer)| auth.peers.get(&peer.ip()))
        .cloned();
    let proof = request.headers_mut().remove(HEADER).and_then(|value| {
        if value.as_bytes().len() > 2048 {
            return None;
        }
        let bytes = URL_SAFE_NO_PAD.decode(value.as_bytes()).ok()?;
        serde_json::from_slice::<SignedUpload>(&bytes).ok()
    });
    let (Some(region), Some(proof)) = (region, proof) else {
        return next.run(request).await;
    };
    let (mut parts, body) = request.into_parts();
    let Ok(body) = axum::body::to_bytes(body, 32 * 1024 * 1024).await else {
        return StatusCode::PAYLOAD_TOO_LARGE.into_response();
    };
    let uri = parts
        .uri
        .path_and_query()
        .map(|p| p.as_str())
        .unwrap_or("/api/posts");
    if auth.valid(&region, &proof, uri, &parts.headers, &body) {
        parts.extensions.insert(proof.image);
    }
    next.run(Request::from_parts(parts, axum::body::Body::from(body)))
        .await
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{Extension, Router, body::Body, routing::post};
    use tower::ServiceExt;

    #[tokio::test]
    async fn proof_requires_exact_body_headers_path_expiry_region_and_actual_peer() {
        let key = [41; 32];
        let auth = UploadAuthenticator::new(
            key,
            [("127.0.0.1".parse().unwrap(), "europe".into())].into(),
        );
        let app = Router::new()
            .route(
                "/api/posts",
                post(|proof: Option<Extension<StoredUpload>>| async move {
                    if proof.is_some() {
                        StatusCode::CREATED
                    } else {
                        StatusCode::FORBIDDEN
                    }
                }),
            )
            .layer(axum::middleware::from_fn_with_state(auth, authenticate));
        let mut headers = HeaderMap::new();
        headers.insert("authorization", "Bearer test".parse().unwrap());
        headers.insert(
            "content-type",
            "multipart/form-data; boundary=test".parse().unwrap(),
        );
        headers.insert("x-client-ip", "192.0.2.1".parse().unwrap());
        let body = Bytes::from_static(b"signed multipart");
        let image = StoredUpload {
            asset_key: format!("europe:{}", uuid::Uuid::new_v4()),
            width: 32,
            height: 32,
            bytes: 1234,
            expires: Utc::now().timestamp() + 60,
        };
        for case in 0..10 {
            let mut reference = image.clone();
            if case == 5 {
                reference.expires = Utc::now().timestamp() - 1;
            }
            if case == 6 {
                reference.asset_key = reference.asset_key.replace("europe:", "america:");
            }
            if case == 7 {
                reference.bytes = 0;
            }
            if case == 8 {
                reference.expires = Utc::now().timestamp() + 300;
            }
            let proof = sign(&key, reference, "/api/posts", &headers, &body);
            let mut request = Request::builder()
                .method("POST")
                .uri(if case == 4 {
                    "/api/posts?changed=1"
                } else {
                    "/api/posts"
                })
                .body(Body::from(if case == 1 {
                    Bytes::from_static(b"changed body")
                } else {
                    body.clone()
                }))
                .unwrap();
            *request.headers_mut() = headers.clone();
            if case == 2 {
                request
                    .headers_mut()
                    .insert("authorization", "Bearer changed".parse().unwrap());
            }
            if case == 9 {
                request
                    .headers_mut()
                    .insert("x-client-ip", "192.0.2.2".parse().unwrap());
            }
            request.headers_mut().insert(HEADER, proof.parse().unwrap());
            request.extensions_mut().insert(ConnectInfo(
                if case == 3 {
                    "127.0.0.2:12345"
                } else {
                    "127.0.0.1:12345"
                }
                .parse::<SocketAddr>()
                .unwrap(),
            ));
            assert_eq!(
                app.clone().oneshot(request).await.unwrap().status(),
                if case == 0 {
                    StatusCode::CREATED
                } else {
                    StatusCode::FORBIDDEN
                },
                "case {case}"
            );
        }
    }
}
