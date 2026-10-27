//! Best-effort cache warming after immutable bytes and metadata have committed.
//! Receiving bytes never authorizes a public image request.
use axum::{
    Router,
    extract::{ConnectInfo, DefaultBodyLimit, Path, Query, Request, State},
    http::StatusCode,
    middleware::{Next, from_fn_with_state},
    response::{IntoResponse, Response},
    routing::post,
};
use bytes::Bytes;
use chrono::{DateTime, Utc};

use serde::Deserialize;
use std::{
    collections::HashMap,
    net::{IpAddr, SocketAddr},
    sync::Arc,
    time::Duration,
};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::sync::Semaphore;

#[derive(Clone)]
pub struct CachePeers {
    targets: Arc<HashMap<String, String>>,
    pending: Arc<Semaphore>,
}

impl CachePeers {
    pub fn from_env() -> Option<Self> {
        let value = std::env::var("ISUREAL_IMAGE_CACHE_PEERS_JSON").unwrap_or_default();
        if value.is_empty() {
            return None;
        }
        let targets: HashMap<String, String> =
            serde_json::from_str(&value).expect("valid cache peer mapping");
        for (region, host) in &targets {
            assert!(
                matches!(
                    (region.as_str(), host.as_str()),
                    ("europe", "192.168.146.10:8080") | ("america", "192.168.147.10:8080")
                ),
                "known regional cache peer"
            );
        }
        Some(Self::new(targets))
    }

    fn new(targets: HashMap<String, String>) -> Self {
        Self {
            targets: Arc::new(targets),
            pending: Arc::new(Semaphore::new(128)),
        }
    }

    pub fn publish(
        &self,
        region: &str,
        local_region: &str,
        id: &str,
        created_at: DateTime<Utc>,
        data: Bytes,
    ) {
        if region == local_region {
            crate::image_api::remember_stored_image(id.to_owned(), created_at, data);
            return;
        }
        let Some(target) = self.targets.get(region) else {
            return;
        };
        let slots = data.len().div_ceil(256 * 1024).clamp(1, 128) as u32;
        let Ok(permit) = self.pending.clone().try_acquire_many_owned(slots) else {
            return;
        };
        if data.len() > 32 * 1024 * 1024 {
            return;
        }
        let target = target.clone();
        let header = format!(
            "POST /internal/astra/image-cache/{id}?createdAtMicros={} HTTP/1.1\r\nHost: {target}\r\nContent-Type: image/jpeg\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
            created_at.timestamp_micros(),
            data.len()
        );
        tokio::spawn(async move {
            let _permit = permit;
            // Cache warming cannot hold up publication or accumulate unbounded
            // uploads. Timeout/failure simply leaves the normal DB-backed path.
            let _ = tokio::time::timeout(Duration::from_secs(2), async {
                // This is a single fixed internal HTTP/1.1 POST, with a numeric
                // content length and server-generated ID/timestamp. No user
                // headers, redirects, proxies or response body are consumed.
                let mut stream = tokio::net::TcpStream::connect(&target).await?;
                stream.set_nodelay(true)?;
                stream.write_all(header.as_bytes()).await?;
                stream.write_all(&data).await?;
                let mut response = [0u8; 128];
                let _ = stream.read(&mut response).await?;
                Ok::<_, std::io::Error>(())
            })
            .await;
        });
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Timestamp {
    created_at_micros: i64,
}

async fn only_writer(State(writer): State<IpAddr>, req: Request, next: Next) -> Response {
    if req
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .is_none_or(|ConnectInfo(peer)| peer.ip() != writer)
    {
        return StatusCode::FORBIDDEN.into_response();
    }
    next.run(req).await
}

async fn receive(
    Path(id): Path<String>,
    Query(timestamp): Query<Timestamp>,
    data: Bytes,
) -> StatusCode {
    if uuid::Uuid::parse_str(&id).is_err() || data.is_empty() {
        return StatusCode::BAD_REQUEST;
    }
    let Some(created_at) = DateTime::from_timestamp_micros(timestamp.created_at_micros) else {
        return StatusCode::BAD_REQUEST;
    };
    crate::image_api::remember_stored_image(id, created_at, data);
    StatusCode::NO_CONTENT
}

pub fn receiver_router(writer: IpAddr) -> Router {
    Router::new()
        .route("/internal/astra/image-cache/{imageId}", post(receive))
        .layer(DefaultBodyLimit::max(32 * 1024 * 1024))
        .layer(from_fn_with_state(writer, only_writer))
}

pub fn receiver_from_env() -> Option<Router> {
    let value = std::env::var("ISUREAL_IMAGE_CACHE_SOURCE_HOST").unwrap_or_default();
    if value.is_empty() {
        return None;
    }
    assert_eq!(
        value, "192.168.145.10",
        "cache source is the existing Asia writer"
    );
    Some(receiver_router(value.parse().unwrap()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, http::Request as HttpRequest};
    use tower::ServiceExt;

    #[tokio::test]
    async fn receiver_requires_actual_writer_socket_and_valid_payload() {
        let router = receiver_router("192.168.145.10".parse().unwrap());
        let id = uuid::Uuid::new_v4();
        for peer in [None, Some("127.0.0.1:1"), Some("192.168.146.10:1")] {
            let mut req = HttpRequest::post(format!(
                "/internal/astra/image-cache/{id}?createdAtMicros=1"
            ))
            .header("X-Forwarded-For", "192.168.145.10")
            .body(Body::from("poison"))
            .unwrap();
            if let Some(peer) = peer {
                req.extensions_mut()
                    .insert(ConnectInfo(peer.parse::<SocketAddr>().unwrap()));
            }
            assert_eq!(
                router.clone().oneshot(req).await.unwrap().status(),
                StatusCode::FORBIDDEN
            );
        }
        for (id, data, expected) in [
            ("bad-id".to_owned(), "bytes", StatusCode::BAD_REQUEST),
            (id.to_string(), "", StatusCode::BAD_REQUEST),
            (id.to_string(), "bytes", StatusCode::NO_CONTENT),
        ] {
            let mut req = HttpRequest::post(format!(
                "/internal/astra/image-cache/{id}?createdAtMicros=1"
            ))
            .body(Body::from(data))
            .unwrap();
            req.extensions_mut().insert(ConnectInfo(
                "192.168.145.10:1".parse::<SocketAddr>().unwrap(),
            ));
            assert_eq!(
                router.clone().oneshot(req).await.unwrap().status(),
                expected
            );
        }
    }

    #[tokio::test]
    async fn asynchronous_push_delivers_exact_immutable_bytes() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (sender, receiver) = tokio::sync::oneshot::channel();
        let sender = Arc::new(std::sync::Mutex::new(Some(sender)));
        let received = sender.clone();
        let router = Router::new().route(
            "/internal/astra/image-cache/{id}",
            post(
                move |Path(id): Path<String>, Query(timestamp): Query<Timestamp>, data: Bytes| {
                    let sender = received.clone();
                    async move {
                        sender
                            .lock()
                            .unwrap()
                            .take()
                            .unwrap()
                            .send((id, timestamp.created_at_micros, data))
                            .unwrap();
                        StatusCode::NO_CONTENT
                    }
                },
            ),
        );
        let server = tokio::spawn(async move {
            axum::serve(listener, router).await.unwrap();
        });
        let peers = CachePeers::new(HashMap::from([("europe".into(), address.to_string())]));
        let id = uuid::Uuid::new_v4().to_string();
        let at = Utc::now();
        let data = Bytes::from_static(b"exact image bytes");
        peers.publish("europe", "asia", &id, at, data.clone());
        assert_eq!(
            tokio::time::timeout(Duration::from_secs(2), receiver)
                .await
                .unwrap()
                .unwrap(),
            (id, at.timestamp_micros(), data)
        );
        server.abort();
    }
    #[tokio::test]
    async fn slow_or_failed_deliveries_have_a_bounded_pending_budget() {
        let peers = CachePeers::new(HashMap::from([("europe".into(), "127.0.0.1:1".into())]));
        let data = Bytes::from(vec![7u8; 1024 * 1024]);
        let id = uuid::Uuid::new_v4().to_string();
        for _ in 0..64 {
            peers.publish("europe", "asia", &id, Utc::now(), data.clone());
        }
        assert_eq!(peers.pending.available_permits(), 0);
        tokio::time::timeout(Duration::from_secs(3), async {
            while peers.pending.available_permits() != 128 {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
    }
}
