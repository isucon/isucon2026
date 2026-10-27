//! Preserve uploaded post bytes at the regional ingress without sending a
//! second image copy back from the writer. The writer remains authoritative.
use std::{convert::Infallible, net::SocketAddr, sync::Arc, time::Duration};

use axum::{
    extract::{Request, State},
    http::{Method, StatusCode, header},
    middleware::Next,
    response::{IntoResponse, Response},
};
use bytes::{Bytes, BytesMut};
use chrono::{DateTime, Utc};
use hyper_client::{Body, Client, body::HttpBody, client::HttpConnector};
use serde::Deserialize;
use tokio::sync::Semaphore;

const BODY_LIMIT: usize = 32 * 1024 * 1024;
const RESPONSE_LIMIT: usize = 64 * 1024;
const SLOT_BYTES: usize = 256 * 1024;

#[derive(Clone)]
pub struct UploadProxy {
    target: SocketAddr,
    client: Client<HttpConnector>,
    pending: Arc<Semaphore>,
}

impl UploadProxy {
    pub fn new(target: SocketAddr) -> Self {
        let mut connector = HttpConnector::new();
        connector.set_nodelay(true);
        Self {
            target,
            client: Client::builder()
                .pool_max_idle_per_host(32)
                .retry_canceled_requests(false)
                .build(connector),
            pending: Arc::new(Semaphore::new(BODY_LIMIT / SLOT_BYTES)),
        }
    }

    pub(crate) fn from_env(region: &str) -> Option<Self> {
        let target = std::env::var("ISUREAL_UPLOAD_PROXY_TARGET").unwrap_or_default();
        if target.is_empty() {
            return None;
        }
        assert!(
            matches!(region, "europe" | "america"),
            "upload cache belongs at regional ingress"
        );
        assert_eq!(
            target, "192.168.145.10:8081",
            "use the existing private writer"
        );
        Some(Self::new(target.parse().unwrap()))
    }

    async fn forward(&self, request: Request) -> Result<Response, anyhow::Error> {
        let length = request
            .headers()
            .get(header::CONTENT_LENGTH)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.parse::<usize>().ok());
        if length.is_some_and(|length| length > BODY_LIMIT) {
            return Ok(StatusCode::PAYLOAD_TOO_LARGE.into_response());
        }
        let slots = length
            .unwrap_or(BODY_LIMIT)
            .div_ceil(SLOT_BYTES)
            .clamp(1, BODY_LIMIT / SLOT_BYTES) as u32;
        let _permit = self.pending.acquire_many(slots).await?;
        let (parts, body) = request.into_parts();
        let body = axum::body::to_bytes(body, BODY_LIMIT).await?;
        let content_type = parts
            .headers
            .get(header::CONTENT_TYPE)
            .and_then(|v| v.to_str().ok())
            .map(str::to_owned);
        let mut upstream = hyper_client::Request::builder().method("POST").uri(format!(
            "http://{}{}",
            self.target,
            parts
                .uri
                .path_and_query()
                .map(|p| p.as_str())
                .unwrap_or("/api/posts")
        ));
        // Copy only headers the post API consumes. The regional identity is
        // derived by the writer from this connection's real peer address.
        for name in ["authorization", "content-type", "x-client-ip", "host"] {
            if let Some(value) = parts.headers.get(name) {
                upstream = upstream.header(name, value.as_bytes());
            }
        }
        let mut upstream = self
            .client
            .request(upstream.body(Body::from(body.clone()))?)
            .await?;
        let status = StatusCode::from_u16(upstream.status().as_u16())?;
        let response_type = upstream
            .headers()
            .get("content-type")
            .map(|v| v.as_bytes().to_vec());
        let cache_control = upstream
            .headers()
            .get("cache-control")
            .map(|v| v.as_bytes().to_vec());
        let mut response_body = BytesMut::new();
        while let Some(chunk) = upstream.body_mut().data().await {
            let chunk = chunk?;
            anyhow::ensure!(
                response_body.len() + chunk.len() <= RESPONSE_LIMIT,
                "oversized writer response"
            );
            response_body.extend_from_slice(&chunk);
        }
        let response_body = response_body.freeze();
        if status == StatusCode::CREATED {
            // A cache parse failure cannot alter a successful authoritative
            // response. Both raw request bytes and writer response stay intact.
            if let Some(content_type) = content_type {
                let _ = warm(&content_type, body, &response_body).await;
            }
        }
        let mut response = (status, response_body).into_response();
        if let Some(value) = response_type {
            response
                .headers_mut()
                .insert(header::CONTENT_TYPE, value.try_into()?);
        }
        if let Some(value) = cache_control {
            response
                .headers_mut()
                .insert(header::CACHE_CONTROL, value.try_into()?);
        }
        Ok(response)
    }
}

pub async fn upload_proxy_middleware(
    State(proxy): State<UploadProxy>,
    request: Request,
    next: Next,
) -> Response {
    if request.method() != Method::POST || request.uri().path() != "/api/posts" {
        return next.run(request).await;
    }
    // Finish forwarding and warming even if the original client disconnects.
    let task = tokio::spawn(async move {
        tokio::time::timeout(Duration::from_secs(45), proxy.forward(request)).await
    });
    match task.await {
        Ok(Ok(Ok(response))) => response,
        _ => (
            StatusCode::BAD_GATEWAY,
            [(header::CONTENT_TYPE, "application/json")],
            "{\"error\":\"upstream_unavailable\"}",
        )
            .into_response(),
    }
}

async fn warm(content_type: &str, body: Bytes, response: &[u8]) -> Result<(), anyhow::Error> {
    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct CreatedPost {
        image_url: String,
        posted_at: String,
    }
    let created: CreatedPost = serde_json::from_slice(response)?;
    let image_id = created
        .image_url
        .strip_prefix("/api/images/")
        .and_then(|path| path.split_once('?').map(|(id, _)| id))
        .ok_or_else(|| anyhow::anyhow!("unexpected image URL"))?;
    uuid::Uuid::parse_str(image_id)?;
    let boundary = multer::parse_boundary(content_type)?;
    let mut multipart = multer::Multipart::new(
        futures_util::stream::once(async { Ok::<_, Infallible>(body) }),
        boundary,
    );
    while let Some(field) = multipart.next_field().await? {
        // Match Bind::form_file: use the first image file with a nonempty name.
        if field.name() == Some("image") && field.file_name().is_some_and(|name| !name.is_empty()) {
            let bytes = field.bytes().await?;
            crate::image_api::remember_stored_image(
                image_id.to_owned(),
                DateTime::parse_from_rfc3339(&created.posted_at)?.with_timezone(&Utc),
                bytes,
            );
            break;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{Router, body::Body as AxumBody, middleware::from_fn_with_state, routing::post};
    use http_body_util::BodyExt;
    use std::sync::Mutex;
    use tower::ServiceExt;

    #[tokio::test]
    async fn forwards_exact_body_credentials_and_status_without_retries() {
        let observed = Arc::new(Mutex::new(Vec::new()));
        let observed2 = observed.clone();
        let backend = Router::new().route(
            "/api/posts",
            post(move |request: Request| {
                let observed = observed2.clone();
                async move {
                    let (parts, body) = request.into_parts();
                    let body = axum::body::to_bytes(body, BODY_LIMIT).await.unwrap();
                    observed
                        .lock()
                        .unwrap()
                        .push((parts.uri.to_string(), parts.headers, body));
                    Response::builder()
                        .status(StatusCode::CONFLICT)
                        .header("content-type", "application/json")
                        .body(AxumBody::from_stream(futures_util::stream::iter([
                            Ok::<_, Infallible>(Bytes::from_static(b"{\"error\":")),
                            Ok(Bytes::from_static(b"\"post_slot_unavailable\"}")),
                        ])))
                        .unwrap()
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let proxy = UploadProxy::new(listener.local_addr().unwrap());
        let backend = tokio::spawn(async move { axum::serve(listener, backend).await.unwrap() });
        let app = Router::new()
            .fallback(|| async { StatusCode::IM_A_TEAPOT })
            .layer(from_fn_with_state(proxy, upload_proxy_middleware));
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/posts?test=1")
                    .header("authorization", "Bearer test-token")
                    .header("x-client-ip", "192.0.2.1")
                    .header("content-type", "application/octet-stream")
                    .header("x-ingress-region", "spoofed")
                    .body(AxumBody::from(vec![0, 1, 2, 255]))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::CONFLICT);
        assert_eq!(response.headers()["content-type"], "application/json");
        assert_eq!(
            response
                .into_body()
                .collect()
                .await
                .unwrap()
                .to_bytes()
                .as_ref(),
            b"{\"error\":\"post_slot_unavailable\"}"
        );
        {
            let entries = observed.lock().unwrap();
            assert_eq!(entries.len(), 1);
            assert_eq!(entries[0].0, "/api/posts?test=1");
            assert_eq!(entries[0].1["authorization"], "Bearer test-token");
            assert_eq!(entries[0].1["x-client-ip"], "192.0.2.1");
            assert!(entries[0].1.get("x-ingress-region").is_none());
            assert_eq!(entries[0].2.as_ref(), &[0, 1, 2, 255]);
        }
        assert_eq!(
            app.clone()
                .oneshot(Request::get("/api/posts").body(AxumBody::empty()).unwrap())
                .await
                .unwrap()
                .status(),
            StatusCode::IM_A_TEAPOT
        );
        assert_eq!(
            app.oneshot(
                Request::post("/api/posts")
                    .header("content-length", BODY_LIMIT + 1)
                    .body(AxumBody::empty())
                    .unwrap()
            )
            .await
            .unwrap()
            .status(),
            StatusCode::PAYLOAD_TOO_LARGE
        );
        assert_eq!(observed.lock().unwrap().len(), 1);
        backend.abort();
    }
}
