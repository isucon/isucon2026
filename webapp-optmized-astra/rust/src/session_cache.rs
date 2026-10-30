//! Read authentication with acknowledged regional invalidation.
//! Leases start before the SQL lookup, never on cache hits. They also bound
//! stale entries if an invalidation cannot be delivered after a committed write.
use std::{
    collections::{HashMap, VecDeque},
    net::SocketAddr,
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

use axum::{
    Extension, Json, Router,
    extract::{ConnectInfo, DefaultBodyLimit, State},
    http::{HeaderMap, Method, StatusCode},
    routing::post,
};
use hyper_client::{Body, Client, client::HttpConnector};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use subtle::ConstantTimeEq;

use crate::session::Session;

pub const LEASE: Duration = Duration::from_secs(5);
const CAPACITY: usize = 8192;
const PATH: &str = "/internal/astra/session-cache";

struct Entry {
    session: Session,
    expires: Instant,
}
#[derive(Default)]
struct Entries {
    generation: u64,
    all_generation: u64,
    user_generations: HashMap<String, u64>,
    hits: u64,
    misses: u64,
    values: HashMap<String, Entry>,
    order: VecDeque<String>,
}

#[derive(Default)]
pub struct SessionCache(Mutex<Entries>);

impl SessionCache {
    pub fn lookup(&self, token: &str) -> (Option<Session>, u64) {
        let mut cache = self.0.lock().unwrap();
        let value = cache
            .values
            .get(token)
            .filter(|e| e.expires > Instant::now())
            .map(|e| e.session.clone());
        if value.is_some() {
            cache.hits += 1;
        } else {
            cache.misses += 1;
        }
        (value, cache.generation)
    }

    fn metrics(&self) -> serde_json::Value {
        let cache = self.0.lock().unwrap();
        serde_json::json!({"metric":"session_cache", "hits":cache.hits, "misses":cache.misses,
            "invalidations":cache.generation, "entries":cache.values.len()})
    }

    pub fn insert(&self, session: Session, generation: u64, started: Instant) {
        let expires = started + LEASE;
        if expires <= Instant::now() {
            return;
        }
        let mut cache = self.0.lock().unwrap();
        // Only changes to this user (or a full reset) can make this lookup's
        // MVCC snapshot stale. Other users must not prevent a cache fill.
        if cache.all_generation > generation
            || cache
                .user_generations
                .get(&session.user_id)
                .copied()
                .unwrap_or(0)
                > generation
        {
            return;
        }
        if !cache.values.contains_key(&session.token_hash) {
            while cache.values.len() >= CAPACITY {
                if let Some(key) = cache.order.pop_front() {
                    cache.values.remove(&key);
                }
            }
            cache.order.push_back(session.token_hash.clone());
        }
        cache
            .values
            .insert(session.token_hash.clone(), Entry { session, expires });
    }

    pub fn invalidate(&self, user: Option<&str>) {
        let mut cache = self.0.lock().unwrap();
        cache.generation = cache.generation.wrapping_add(1);
        match user {
            Some(user) if cache.user_generations.len() < 65536 => {
                let generation = cache.generation;
                cache.user_generations.insert(user.to_owned(), generation);
                cache.values.retain(|_, e| e.session.user_id != user);
            }
            _ => {
                // Bound version metadata while rejecting all pre-reset fills.
                cache.all_generation = cache.generation;
                cache.user_generations.clear();
                cache.values.clear();
            }
        }
        // Keep eviction metadata bounded as tokens are invalidated/replaced.
        let Entries { values, order, .. } = &mut *cache;
        order.retain(|key| values.contains_key(key));
    }
}

#[derive(Clone)]
pub struct ChangedSessionUser(pub String);

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Invalidation {
    pub user: Option<String>,
    pub lsn: String,
}

pub struct SessionCacheCoordinator {
    pub cache: SessionCache,
    primary: PgPool,
    reader: PgPool,
    token: String,
    peers: Vec<String>,
    client: Client<HttpConnector>,
    local_primary: bool,
}

impl SessionCacheCoordinator {
    pub async fn from_env(primary: PgPool, reader: &PgPool, password: &str) -> Option<Arc<Self>> {
        let local_primary = std::env::var("ISUREAL_ASIA_SESSION_CACHE").as_deref() == Ok("1");
        if local_primary && std::env::var("ISUREAL_REGION").as_deref() != Ok("asia") {
            return None;
        }
        if !local_primary && std::env::var("ISUREAL_SESSION_CACHE").as_deref() != Ok("1") {
            return None;
        }
        let peers: Vec<String> = serde_json::from_str(
            &std::env::var("ISUREAL_SESSION_CACHE_PEERS").expect("session cache peers"),
        )
        .expect("session cache peers JSON");
        assert_eq!(
            peers,
            [
                "192.168.177.10:8080",
                "192.168.178.10:8080",
                "192.168.179.10:8080"
            ]
        );
        let (primary, reader) = if local_primary {
            let primary_read: bool = sqlx::query_scalar("SELECT NOT pg_is_in_recovery()")
                .fetch_one(reader)
                .await
                .expect("check authoritative cache reader");
            assert!(primary_read, "Asia session cache requires primary reads");
            (primary, reader.clone())
        } else {
            let primary = crate::db_pool::options(2)
                .connect_with(
                    (*primary.connect_options())
                        .clone()
                        .application_name("astra_cache_source"),
                )
                .await
                .expect("cache source pool");
            let reader = crate::db_pool::options(2)
                .connect_with(
                    (*reader.connect_options())
                        .clone()
                        .application_name("astra_cache_invalidation"),
                )
                .await
                .expect("cache replay pool");
            (primary, reader)
        };
        let token = hex::encode(Sha256::digest(
            format!("astra-session-cache-v1:{password}").as_bytes(),
        ));
        let coordinator = Arc::new(if local_primary {
            Self::new_local_primary(primary, reader, token, vec![peers[0].clone()])
        } else {
            Self::new(primary, reader, token, peers)
        });
        let weak = Arc::downgrade(&coordinator);
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_secs(10)).await;
                let Some(coordinator) = weak.upgrade() else {
                    break;
                };
                eprintln!("{}", coordinator.cache.metrics());
            }
        });
        Some(coordinator)
    }

    pub fn new(primary: PgPool, reader: PgPool, token: String, peers: Vec<String>) -> Self {
        let mut connector = HttpConnector::new();
        connector.set_nodelay(true);
        Self {
            local_primary: false,
            cache: SessionCache::default(),
            primary,
            reader,
            token,
            peers,
            client: Client::builder()
                .pool_max_idle_per_host(8)
                .retry_canceled_requests(false)
                .build(connector),
        }
    }

    /// All publishers and this reader use the same primary. Commit precedes
    /// invalidation, so no replica replay or LSN query is necessary.
    pub fn new_local_primary(
        primary: PgPool,
        reader: PgPool,
        token: String,
        peers: Vec<String>,
    ) -> Self {
        let mut coordinator = Self::new(primary, reader, token, peers);
        coordinator.local_primary = true;
        coordinator
    }

    pub fn receiver(self: &Arc<Self>) -> Router {
        Router::new()
            .route(PATH, post(receive))
            .layer(DefaultBodyLimit::max(1024))
            .with_state(self.clone())
    }

    pub async fn publish(&self, user: Option<String>) -> Result<(), String> {
        tokio::time::timeout(Duration::from_secs(5), async {
            let lsn: String = if self.local_primary {
                "0/0".to_owned()
            } else {
                sqlx::query_scalar("SELECT pg_current_wal_insert_lsn()::text")
                    .fetch_one(&self.primary)
                    .await
                    .map_err(|e| e.to_string())?
            };
            let body =
                serde_json::to_vec(&Invalidation { user, lsn }).map_err(|e| e.to_string())?;
            let outcomes = futures_util::future::join_all(self.peers.iter().map(|peer| {
                let body = body.clone();
                async move {
                    let request = hyper_client::Request::post(format!("http://{peer}{PATH}"))
                        .header("content-type", "application/json")
                        .header("x-astra-session-cache", &self.token)
                        .body(Body::from(body.clone()))
                        .map_err(|e| e.to_string())?;
                    let response = self
                        .client
                        .request(request)
                        .await
                        .map_err(|e| e.to_string())?;
                    if response.status() != hyper_client::StatusCode::NO_CONTENT {
                        return Err("cache invalidation rejected".to_owned());
                    }
                    hyper_client::body::to_bytes(response.into_body())
                        .await
                        .map_err(|e| e.to_string())?;
                    Ok::<(), String>(())
                }
            }))
            .await;
            for outcome in outcomes {
                outcome?;
            }
            Ok(())
        })
        .await
        .map_err(|_| "cache invalidation timeout".to_owned())?
    }

    pub async fn apply(&self, message: Invalidation) -> Result<(), String> {
        if self.local_primary {
            self.cache.invalidate(message.user.as_deref());
            return Ok(());
        }
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                let ready: bool = sqlx::query_scalar("SELECT NOT pg_is_in_recovery() OR coalesce(pg_last_wal_replay_lsn() >= $1::pg_lsn, false)")
                    .bind(&message.lsn).fetch_one(&self.reader).await.map_err(|e| e.to_string())?;
                if ready {break;}
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
            self.cache.invalidate(message.user.as_deref());
            Ok(())
        }).await.map_err(|_| "cache replica replay timeout".to_owned())?
    }
}

async fn receive(
    State(coordinator): State<Arc<SessionCacheCoordinator>>,
    ConnectInfo(peer): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    Json(message): Json<Invalidation>,
) -> StatusCode {
    if !matches!(
        peer.ip().to_string().as_str(),
        "192.168.177.10" | "192.168.178.10" | "192.168.179.10"
    ) {
        return StatusCode::FORBIDDEN;
    }
    let supplied = headers
        .get("x-astra-session-cache")
        .map(|v| v.as_bytes())
        .unwrap_or_default();
    if !bool::from(supplied.ct_eq(coordinator.token.as_bytes())) {
        return StatusCode::FORBIDDEN;
    }
    match coordinator.apply(message).await {
        Ok(()) => StatusCode::NO_CONTENT,
        Err(_) => StatusCode::SERVICE_UNAVAILABLE,
    }
}

/// Successful and failed responses on these routes can follow committed work.
pub fn changed_user(
    method: &Method,
    path: &str,
    response: &axum::response::Response,
) -> Option<Option<String>> {
    if method == Method::POST && path == "/api/initialize" {
        return Some(None);
    }
    if method == Method::POST && path == "/api/auth/login" {
        return response
            .extensions()
            .get::<ChangedSessionUser>()
            .map(|u| Some(u.0.clone()));
    }
    if (method == Method::POST && matches!(path, "/api/auth/logout" | "/api/me/account-deletion"))
        || (method == Method::PATCH && path == "/api/me")
    {
        return response
            .extensions()
            .get::<Session>()
            .map(|s| Some(s.user_id.clone()));
    }
    None
}

pub fn coordinator(req: &axum::extract::Request) -> Option<Arc<SessionCacheCoordinator>> {
    req.extensions()
        .get::<Arc<SessionCacheCoordinator>>()
        .cloned()
}

pub fn layer(coordinator: Arc<SessionCacheCoordinator>) -> Extension<Arc<SessionCacheCoordinator>> {
    Extension(coordinator)
}
