use axum::{
    Extension, Router,
    body::Body,
    extract::ConnectInfo,
    http::{Request, StatusCode},
    middleware::from_fn_with_state,
    routing::get,
};
use isureal::{
    HandlerContext, config_from_env,
    session::{Session, session_middleware},
    session_cache::{Invalidation, LEASE, SessionCache, SessionCacheCoordinator},
};
use sha2::{Digest, Sha256};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{
    str::FromStr,
    sync::Arc,
    time::{Duration, Instant},
};
use tower::ServiceExt;

fn session(user: &str, token: &str) -> Session {
    Session {
        user_id: user.into(),
        token_hash: token.into(),
        region: "asia".into(),
        created_at: chrono::Utc::now(),
        token: String::new(),
    }
}

#[test]
fn invalidation_blocks_late_lookup_results_and_hits_do_not_extend_the_lease() {
    let cache = SessionCache::default();
    let (_, generation) = cache.lookup("token");
    cache.insert(session("user", "token"), generation, Instant::now());
    assert!(cache.lookup("token").0.is_some());
    cache.invalidate(Some("user"));
    cache.insert(session("user", "token"), generation, Instant::now());
    assert!(cache.lookup("token").0.is_none());
    let (_, generation) = cache.lookup("token");
    cache.insert(
        session("user", "token"),
        generation,
        Instant::now() - LEASE - Duration::from_secs(1),
    );
    assert!(cache.lookup("token").0.is_none());
    cache.insert(
        session("short", "short"),
        generation,
        Instant::now() - LEASE + Duration::from_millis(20),
    );
    assert!(cache.lookup("short").0.is_some());
    std::thread::sleep(Duration::from_millis(30));
    assert!(cache.lookup("short").0.is_none());
    cache.insert(session("other", "other"), generation, Instant::now());
    cache.invalidate(Some("user"));
    assert!(cache.lookup("other").0.is_some());
    // Unrelated writes during the SQL lookup must not discard its result.
    cache.insert(session("unrelated", "late"), generation, Instant::now());
    assert!(cache.lookup("late").0.is_some());
    cache.invalidate(None);
    assert!(cache.lookup("other").0.is_none());
    cache.insert(session("unrelated", "late"), generation, Instant::now());
    assert!(cache.lookup("late").0.is_none());
}

async fn request(app: &Router, method: &str, token: &str) -> StatusCode {
    app.clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri("/read")
                .header("authorization", format!("Bearer {token}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap()
        .status()
}

#[tokio::test]
async fn read_cache_skips_sql_but_mutations_and_invalidated_sessions_use_fresh_authentication() {
    check_read_cache(false).await;
    check_read_cache(true).await;
}

async fn check_read_cache(local_primary: bool) {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_session_cache_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(1)
        .acquire_timeout(Duration::from_millis(200))
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE users(id text PRIMARY KEY,region text,status text); CREATE TABLE login_sessions(user_id text,token_hash text,created_at timestamptz DEFAULT now(),active boolean); INSERT INTO users VALUES ('user','asia','active')").execute(&db).await.unwrap();
    let hash = hex::encode(Sha256::digest(b"token"));
    sqlx::query("INSERT INTO login_sessions(user_id,token_hash,active) VALUES ('user',$1,true)")
        .bind(&hash)
        .execute(&db)
        .await
        .unwrap();
    let constructor = if local_primary {
        SessionCacheCoordinator::new_local_primary
    } else {
        SessionCacheCoordinator::new
    };
    let cache = Arc::new(constructor(
        db.clone(),
        db.clone(),
        "test-secret".into(),
        vec![],
    ));
    let context = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: false,
        read_fence: None,
        image_store: None,
    });
    let app = Router::new()
        .route(
            "/read",
            get(|Extension(session): Extension<Session>| async move { session.user_id })
                .post(|| async { StatusCode::OK }),
        )
        .route_layer(from_fn_with_state((context, true), session_middleware))
        .layer(Extension(cache.clone()));
    assert_eq!(request(&app, "GET", "token").await, StatusCode::OK);
    // Holding the only connection proves a cached read does not touch SQL.
    let held = db.acquire().await.unwrap();
    assert_eq!(request(&app, "GET", "token").await, StatusCode::OK);
    assert_eq!(
        request(&app, "POST", "token").await,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    drop(held);
    sqlx::query("UPDATE login_sessions SET active=false")
        .execute(&db)
        .await
        .unwrap();
    cache
        .apply(Invalidation {
            user: Some("user".into()),
            lsn: "0/0".into(),
        })
        .await
        .unwrap();
    assert_eq!(
        request(&app, "GET", "token").await,
        StatusCode::UNAUTHORIZED
    );
    sqlx::query("UPDATE login_sessions SET active=true")
        .execute(&db)
        .await
        .unwrap();
    assert_eq!(request(&app, "GET", "token").await, StatusCode::OK);
    sqlx::query("UPDATE users SET region='america'")
        .execute(&db)
        .await
        .unwrap();
    cache
        .apply(Invalidation {
            user: Some("user".into()),
            lsn: "0/0".into(),
        })
        .await
        .unwrap();
    assert_eq!(request(&app, "GET", "token").await, StatusCode::OK);
    assert_eq!(cache.cache.lookup(&hash).0.unwrap().region, "america");
    sqlx::query("UPDATE users SET status='deleted'")
        .execute(&db)
        .await
        .unwrap();
    cache
        .apply(Invalidation {
            user: Some("user".into()),
            lsn: "0/0".into(),
        })
        .await
        .unwrap();
    assert_eq!(
        request(&app, "GET", "token").await,
        StatusCode::UNAUTHORIZED
    );
    // Only a known VM socket and the shared token may invalidate state.
    for (ip, key, status) in [
        ("127.0.0.1:1", "test-secret", StatusCode::FORBIDDEN),
        ("192.168.177.10:1", "wrong", StatusCode::FORBIDDEN),
        ("192.168.177.10:1", "test-secret", StatusCode::NO_CONTENT),
    ] {
        let mut req = Request::post("/internal/astra/session-cache")
            .header("content-type", "application/json")
            .header("x-astra-session-cache", key)
            .body(Body::from(r#"{"user":null,"lsn":"0/0"}"#))
            .unwrap();
        req.extensions_mut()
            .insert(ConnectInfo(ip.parse::<std::net::SocketAddr>().unwrap()));
        assert_eq!(
            cache.receiver().oneshot(req).await.unwrap().status(),
            status
        );
    }
    drop(app);
    drop(cache);
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}

#[tokio::test]
async fn invalidation_acknowledgement_waits_for_actual_replica_replay() {
    let (Ok(primary), Ok(replica)) = (
        std::env::var("ISUREAL_FENCE_PRIMARY_DSN"),
        std::env::var("ISUREAL_FENCE_REPLICA1_DSN"),
    ) else {
        return;
    };
    let primary = PgPoolOptions::new()
        .connect_with(
            PgConnectOptions::from_str(&primary)
                .unwrap()
                .options([("synchronous_commit", "local")]),
        )
        .await
        .unwrap();
    let replica = PgPool::connect(&replica).await.unwrap();
    let coordinator = Arc::new(SessionCacheCoordinator::new(
        primary.clone(),
        replica.clone(),
        String::new(),
        vec![],
    ));
    let table = format!("session_cache_replay_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE TABLE {table}(n integer)"))
        .execute(&primary)
        .await
        .unwrap();
    sqlx::query("SELECT pg_wal_replay_pause()")
        .execute(&replica)
        .await
        .unwrap();
    let run: Result<_, sqlx::Error> = async {
        sqlx::query(&format!("INSERT INTO {table} VALUES(1)"))
            .execute(&primary)
            .await?;
        let lsn: String = sqlx::query_scalar("SELECT pg_current_wal_insert_lsn()::text")
            .fetch_one(&primary)
            .await?;
        let (_, generation) = coordinator.cache.lookup("token");
        coordinator
            .cache
            .insert(session("user", "token"), generation, Instant::now());
        let target = coordinator.clone();
        let pending = tokio::spawn(async move {
            target
                .apply(Invalidation {
                    user: Some("user".into()),
                    lsn,
                })
                .await
        });
        tokio::time::sleep(Duration::from_millis(100)).await;
        let prematurely_finished = pending.is_finished();
        Ok((pending, generation, prematurely_finished))
    }
    .await;
    // Resume before assertions, including when setup failed.
    sqlx::query("SELECT pg_wal_replay_resume()")
        .execute(&replica)
        .await
        .unwrap();
    let (pending, generation, prematurely_finished) = run.unwrap();
    assert!(
        !prematurely_finished,
        "a paused replica must not acknowledge invalidation"
    );
    pending.await.unwrap().unwrap();
    assert!(coordinator.cache.lookup("token").0.is_none());
    coordinator
        .cache
        .insert(session("user", "token"), generation, Instant::now());
    assert!(coordinator.cache.lookup("token").0.is_none());
    sqlx::query(&format!("DROP TABLE {table}"))
        .execute(&primary)
        .await
        .unwrap();
    drop(coordinator);
    primary.close().await;
    replica.close().await;
}

#[tokio::test]
async fn mutation_response_waits_for_every_regional_invalidation() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let db = PgPool::connect(&dsn).await.unwrap();
    let gate = Arc::new(tokio::sync::Notify::new());
    let mut peers = Vec::new();
    let mut caches = Vec::new();
    let mut servers = Vec::new();
    for i in 0..3 {
        let cache = Arc::new(SessionCacheCoordinator::new(
            db.clone(),
            db.clone(),
            "test".into(),
            vec![],
        ));
        cache
            .cache
            .insert(session("user", "token"), 0, Instant::now());
        caches.push(cache.clone());
        let gate = gate.clone();
        let receiver = Router::new().route(
            "/internal/astra/session-cache",
            axum::routing::post(move |axum::Json(message): axum::Json<Invalidation>| {
                let cache = cache.clone();
                let gate = gate.clone();
                async move {
                    if i == 2 {
                        gate.notified().await;
                    }
                    cache.apply(message).await.unwrap();
                    StatusCode::NO_CONTENT
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        peers.push(listener.local_addr().unwrap().to_string());
        servers.push(tokio::spawn(async move {
            axum::serve(listener, receiver).await.unwrap()
        }));
    }
    let publisher = Arc::new(SessionCacheCoordinator::new(
        db.clone(),
        db.clone(),
        "test".into(),
        peers,
    ));
    let app = mutation_app(publisher);
    let response = tokio::spawn(async move {
        app.oneshot(
            Request::post("/api/auth/login")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap()
    });
    tokio::time::sleep(Duration::from_millis(100)).await;
    let returned_early = response.is_finished();
    assert!(caches[0].cache.lookup("token").0.is_none());
    assert!(caches[1].cache.lookup("token").0.is_none());
    assert!(caches[2].cache.lookup("token").0.is_some());
    gate.notify_one();
    assert!(!returned_early);
    assert_eq!(response.await.unwrap().status(), StatusCode::OK);
    assert!(caches[2].cache.lookup("token").0.is_none());
    for server in servers {
        server.abort();
        let _ = server.await;
    }
    drop(caches);
    db.close().await;
}

fn mutation_app(publisher: Arc<SessionCacheCoordinator>) -> Router {
    use axum::response::IntoResponse;
    Router::new()
        .route(
            "/api/auth/login",
            axum::routing::post(|| async {
                let mut response = StatusCode::OK.into_response();
                response
                    .extensions_mut()
                    .insert(isureal::session_cache::ChangedSessionUser("user".into()));
                response
            }),
        )
        .layer(from_fn_with_state(
            None::<isureal::write_fence::WriteFence>,
            isureal::write_fence::write_fence_middleware,
        ))
        .layer(Extension(publisher))
}

#[tokio::test]
async fn unreachable_peer_cannot_leave_valid_old_leases_after_the_error_response() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let db = PgPool::connect(&dsn).await.unwrap();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let unreachable = listener.local_addr().unwrap().to_string();
    drop(listener);
    let peer = SessionCache::default();
    let started = Instant::now();
    peer.insert(session("user", "token"), 0, started);
    let publisher = Arc::new(SessionCacheCoordinator::new(
        db.clone(),
        db.clone(),
        "test".into(),
        vec![unreachable],
    ));
    let response = tokio::time::timeout(
        Duration::from_secs(8),
        mutation_app(publisher).oneshot(
            Request::post("/api/auth/login")
                .body(Body::empty())
                .unwrap(),
        ),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert!(started.elapsed() >= LEASE);
    assert!(peer.lookup("token").0.is_none());
    db.close().await;
}

#[tokio::test]
async fn local_primary_invalidation_waits_for_ack_without_any_database_connection() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let db = PgPoolOptions::new()
        .max_connections(1)
        .connect(&dsn)
        .await
        .unwrap();
    let cache = Arc::new(SessionCacheCoordinator::new_local_primary(
        db.clone(),
        db.clone(),
        "test".into(),
        vec![],
    ));
    cache
        .cache
        .insert(session("user", "token"), 0, Instant::now());
    let gate = Arc::new(tokio::sync::Notify::new());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap().to_string();
    let target = cache.clone();
    let receiver_gate = gate.clone();
    let receiver = Router::new().route(
        "/internal/astra/session-cache",
        axum::routing::post(move |axum::Json(message): axum::Json<Invalidation>| {
            let cache = target.clone();
            let gate = receiver_gate.clone();
            async move {
                gate.notified().await;
                cache.apply(message).await.unwrap();
                StatusCode::NO_CONTENT
            }
        }),
    );
    let server = tokio::spawn(async move {
        axum::serve(listener, receiver).await.unwrap();
    });
    let publisher = Arc::new(SessionCacheCoordinator::new_local_primary(
        db.clone(),
        db.clone(),
        "test".into(),
        vec![address],
    ));
    let held = db.acquire().await.unwrap();
    let response = tokio::spawn(async move {
        mutation_app(publisher)
            .oneshot(
                Request::post("/api/auth/login")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap()
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    let returned_early = response.is_finished();
    assert!(cache.cache.lookup("token").0.is_some());
    gate.notify_one();
    assert!(!returned_early);
    assert_eq!(
        tokio::time::timeout(Duration::from_secs(1), response)
            .await
            .unwrap()
            .unwrap()
            .status(),
        StatusCode::OK
    );
    assert!(cache.cache.lookup("token").0.is_none());
    drop(held);
    server.abort();
    let _ = server.await;
    db.close().await;
}
