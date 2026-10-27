//! Private write worker: preserve the original ingress server's region while
//! executing database transactions next to the primary over the normal network.
use std::collections::HashMap;
use std::net::{IpAddr, SocketAddr};
use std::sync::Arc;

use axum::extract::{ConnectInfo, Request, State};
use axum::http::{Method, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use crate::user::valid_region;

#[derive(Clone)]
pub(crate) struct WriteIngress(Arc<HashMap<IpAddr, String>>);

#[derive(Clone)]
pub(crate) struct IngressRegion(pub String);

impl WriteIngress {
    pub(crate) fn from_env() -> Option<Self> {
        let json = std::env::var("ISUREAL_WRITE_INGRESS_JSON").ok()?;
        if json.is_empty() {
            return None;
        }
        let peers: HashMap<IpAddr, String> =
            serde_json::from_str(&json).expect("valid private write ingress mapping");
        assert!(
            !peers.is_empty(),
            "private write worker requires known peers"
        );
        assert!(
            peers.values().all(|region| valid_region(region)),
            "valid ingress regions"
        );
        Some(Self(Arc::new(peers)))
    }
}

pub(crate) async fn write_ingress_middleware(
    State(policy): State<WriteIngress>,
    mut req: Request,
    next: Next,
) -> Response {
    let region = req
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .and_then(|ConnectInfo(peer)| policy.0.get(&peer.ip()));
    let Some(region) = region else {
        return StatusCode::FORBIDDEN.into_response();
    };
    if matches!(*req.method(), Method::GET | Method::HEAD) {
        return StatusCode::METHOD_NOT_ALLOWED.into_response();
    }
    let region = region.clone();
    // Derive this solely from the peer socket. Public headers cannot impersonate
    // a different regional ingress or bypass the normal session-region check.
    req.extensions_mut().insert(IngressRegion(region));
    next.run(req).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::http::Request as HttpRequest;
    use axum::{Router, body::Body, middleware::from_fn_with_state, routing::post};
    use tower::ServiceExt;

    fn app() -> Router {
        let policy = WriteIngress(Arc::new(HashMap::from([
            ("192.168.146.10".parse().unwrap(), "europe".into()),
            ("192.168.147.10".parse().unwrap(), "america".into()),
        ])));
        Router::new()
            .route(
                "/write",
                post(|req: Request| async move {
                    req.extensions().get::<IngressRegion>().unwrap().0.clone()
                }),
            )
            .layer(from_fn_with_state(policy, write_ingress_middleware))
    }

    #[tokio::test]
    async fn only_socket_peer_selects_original_ingress_region() {
        use http_body_util::BodyExt;
        let mut request = HttpRequest::builder()
            .method("POST")
            .uri("/write")
            .header("X-Ingress-Region", "america")
            .header("X-Country-Code", "US")
            .body(Body::empty())
            .unwrap();
        request.extensions_mut().insert(ConnectInfo(
            "192.168.146.10:1234".parse::<SocketAddr>().unwrap(),
        ));
        let response = app().oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.into_body().collect().await.unwrap().to_bytes(),
            "europe"
        );
        for peer in [None, Some("127.0.0.1:1234"), Some("192.168.145.10:1234")] {
            let mut request = HttpRequest::builder()
                .method("POST")
                .uri("/write")
                .header("X-Ingress-Region", "europe")
                .body(Body::empty())
                .unwrap();
            if let Some(peer) = peer {
                request
                    .extensions_mut()
                    .insert(ConnectInfo(peer.parse::<SocketAddr>().unwrap()));
            }
            assert_eq!(
                app().oneshot(request).await.unwrap().status(),
                StatusCode::FORBIDDEN
            );
        }
        let mut request = HttpRequest::builder()
            .method("GET")
            .uri("/write")
            .body(Body::empty())
            .unwrap();
        request.extensions_mut().insert(ConnectInfo(
            "192.168.147.10:1234".parse::<SocketAddr>().unwrap(),
        ));
        assert_eq!(
            app().oneshot(request).await.unwrap().status(),
            StatusCode::METHOD_NOT_ALLOWED
        );
    }
}

#[cfg(test)]
mod region_tests {
    use super::*;
    use crate::{HandlerContext, config_from_env, region::region_middleware, session::Session};
    use axum::{
        Extension, Router, body::Body, http::Request as HttpRequest,
        middleware::from_fn_with_state, routing::post,
    };
    use chrono::Utc;
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    fn app(user_region: &str, private: bool) -> Router {
        let mut config = config_from_env();
        config.region = "asia".into();
        let h = Arc::new(HandlerContext {
            db: PgPoolOptions::new()
                .connect_lazy("postgres://postgres@127.0.0.1:1/unreachable")
                .unwrap(),
            config,
            ip_bans: Default::default(),
            notifications: Default::default(),
            read_fence: None,
            image_store: None,
        });
        let session = Session {
            user_id: "user".into(),
            region: user_region.into(),
            token: String::new(),
            token_hash: String::new(),
            created_at: Utc::now(),
        };
        let app = Router::new()
            .route("/write", post(|| async { StatusCode::OK }))
            .route_layer(from_fn_with_state(h, region_middleware))
            .layer(Extension(session));
        if private {
            let policy = WriteIngress(Arc::new(HashMap::from([
                ("192.168.146.10".parse().unwrap(), "europe".into()),
                ("192.168.147.10".parse().unwrap(), "america".into()),
            ])));
            app.layer(from_fn_with_state(policy, write_ingress_middleware))
        } else {
            app
        }
    }

    #[tokio::test]
    async fn forwarding_preserves_ingress_region_and_public_server_stays_asia() {
        for (private, user_region, peer, expected) in [
            (true, "europe", "192.168.146.10:1234", StatusCode::OK),
            (true, "america", "192.168.147.10:1234", StatusCode::OK),
            (true, "asia", "192.168.146.10:1234", StatusCode::FORBIDDEN),
            (true, "europe", "192.168.147.10:1234", StatusCode::FORBIDDEN),
            (false, "asia", "192.168.146.10:1234", StatusCode::OK),
            (
                false,
                "europe",
                "192.168.146.10:1234",
                StatusCode::FORBIDDEN,
            ),
        ] {
            let mut req = HttpRequest::builder()
                .method("POST")
                .uri("/write")
                .header("X-Ingress-Region", user_region)
                .body(Body::empty())
                .unwrap();
            req.extensions_mut()
                .insert(ConnectInfo(peer.parse::<SocketAddr>().unwrap()));
            let response = app(user_region, private).oneshot(req).await.unwrap();
            assert_eq!(
                response.status(),
                expected,
                "private={private}, user_region={user_region}, peer={peer}"
            );
        }
    }
}
