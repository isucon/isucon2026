use std::any::Any;
use std::io;
use std::net::SocketAddr;
use std::str::FromStr;
use std::sync::Arc;

use axum::Router;
use axum::extract::Request;
use axum::http::StatusCode;
use axum::middleware::{Next, from_fn, from_fn_with_state};
use axum::response::{IntoResponse, Response};
use axum::routing::{delete, get, patch, post, put};
use axum::serve::ListenerExt;
use sqlx::PgPool;
use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
use tokio::net::TcpListener;
use tower::ServiceBuilder;
use tower_http::catch_panic::CatchPanicLayer;

use crate::account_deletion_api::delete_account_handler;
use crate::action_log::{ActionLog, action_log_middleware};
use crate::admin_api::{admin_login_handler, admin_logout_handler};
use crate::admin_posting_window_api::open_posting_window_handler;
use crate::admin_session::admin_session_middleware;
use crate::admin_user_api::{certify_user_handler, delete_user_certification_handler};
use crate::api_error::{Error, HttpError};
use crate::comment_api::{create_comment_handler, delete_comment_handler, get_comments_handler};
use crate::config::Config;
use crate::export_api::export_posts_handler;
use crate::feed_api::get_feed_handler;
use crate::friend_request_api::{create_friend_request_handler, transition_friend_request_handler};
use crate::image_api::get_image_handler;
use crate::initialize::initialize_handler;
use crate::ip_ban::{IpBanList, ip_ban_middleware};
use crate::memory_api::{get_memories_by_date_handler, get_memories_handler};
use crate::native_write_fence::NativeWriteFence;
use crate::notification_api::{NotificationHub, get_notifications_handler};
use crate::post_api::{
    create_post_handler, delete_post_handler, get_my_current_post_handler, get_post_handler,
    update_post_handler,
};
use crate::posting_window_api::get_current_posting_window_handler;
use crate::profile_image_api::{delete_profile_image_handler, put_profile_image_handler};
use crate::read_fence::{ReadFence, read_fence_middleware};
use crate::realmoji_api::{
    delete_real_moji_handler, get_real_mojis_handler, replace_real_moji_handler,
};
use crate::region::region_middleware;
use crate::relationship_api::{
    block_handler, delete_friendship_handler, follow_handler, unblock_handler, unfollow_handler,
};
use crate::relationship_list_api::get_relationships_handler;
use crate::session::session_middleware;
use crate::user::valid_region;
use crate::user_api::{
    get_me_handler, login_handler, logout_handler, register_handler, update_me_handler,
};
use crate::user_read_api::{get_user_handler, search_users_handler};
use crate::write_fence::{WriteFence, write_fence_middleware};
use crate::write_ingress::{WriteIngress, write_ingress_middleware};

const TWO_STANDBYS: &str = "FIRST 2 (astra_europe, astra_america)";
const THREE_STANDBYS: &str = "FIRST 3 (astra_europe, astra_america, astra_asia)";

pub struct HandlerContext {
    pub db: PgPool,
    pub config: Config,
    pub ip_bans: IpBanList,
    pub notifications: NotificationHub,
    pub notification_batch_writes: bool,
    pub read_fence: Option<ReadFence>,
    pub image_store: Option<crate::image_store::ImageStore>,
}

pub async fn new_server(config: Config) -> Router {
    crate::image_signature::initialize_signature_cache();
    crate::image_api::initialize_image_cache();
    if !valid_region(&config.region) {
        panic!("invalid server region: {}", config.region);
    }

    let options = match PgConnectOptions::from_str(&config.database_dsn()) {
        Ok(options) => options.statement_cache_capacity(512),
        Err(err) => panic!("failed to open database: {err}"),
    };
    let db = match crate::db_pool::options(crate::db_pool::metadata_connections())
        .connect_with(options.clone())
        .await
    {
        Ok(db) => db,
        Err(err) => panic!("failed to ping database: {err}"),
    };

    let batch_writes =
        std::env::var("ISUREAL_NOTIFICATION_BATCH_WRITES").unwrap_or_else(|_| "0".into());
    assert!(matches!(batch_writes.as_str(), "0" | "1"));
    let notification_batch_writes = batch_writes == "1";
    let image_store = crate::image_store::ImageStore::from_env(&config).await;
    if image_store.is_none() {
        let external: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM image_objects WHERE asset_key IS NOT NULL)",
        )
        .fetch_one(&db)
        .await
        .expect("image reference schema is installed");
        assert!(
            !external,
            "retain image store readers while published external images exist"
        );
    }
    let action_log = ActionLog::new(&config.log_level, io::stdout());
    let handler_context = Arc::new(HandlerContext {
        db,
        config,
        ip_bans: IpBanList::default(),
        notifications: NotificationHub::default(),
        notification_batch_writes,
        read_fence: None,
        image_store,
    });
    let write_fence = match std::env::var("ISUREAL_WRITE_FENCE_HOSTS") {
        Ok(hosts) if !hosts.is_empty() => {
            let hosts: Vec<_> = hosts.split(',').collect();
            assert_eq!(
                hosts.len(),
                2,
                "write fences require both regional standbys"
            );
            assert_ne!(hosts[0], hosts[1], "write fence hosts must be distinct");
            let mode: String = sqlx::query_scalar("SHOW synchronous_commit")
                .fetch_one(&handler_context.db)
                .await
                .expect("read commit mode");
            assert!(
                matches!(mode.as_str(), "local" | "on" | "remote_apply"),
                "write fences require durable primary commits"
            );
            let fence_mode =
                std::env::var("ISUREAL_WRITE_FENCE_MODE").unwrap_or_else(|_| "poll".into());
            if fence_mode == "native" {
                let standbys: String = sqlx::query_scalar("SHOW synchronous_standby_names")
                    .fetch_one(&handler_context.db)
                    .await
                    .expect("read synchronous standbys");
                assert!(
                    matches!(standbys.as_str(), TWO_STANDBYS | THREE_STANDBYS),
                    "native write fences require all configured synchronous standbys"
                );
                let table_exists: bool = sqlx::query_scalar(
                    "SELECT to_regclass('astra_visibility_barriers') IS NOT NULL",
                )
                .fetch_one(&handler_context.db)
                .await
                .expect("check barrier table");
                assert!(table_exists, "install visibility barrier migration first");
                // Replica acknowledgements can take a regional RTT. Keep their
                // bounded connections separate from application transactions so
                // queued mutations cannot delay the barrier that releases them.
                let fence_db =
                    crate::db_pool::options(crate::native_write_fence::VISIBILITY_BATCHES as u32)
                        .connect_with(options.clone().application_name("astra_visibility"))
                        .await
                        .expect("connect dedicated visibility pool");
                Some(WriteFence::Native(NativeWriteFence::new(
                    fence_db,
                    std::time::Duration::from_secs(30),
                )))
            } else {
                assert_eq!(fence_mode, "poll", "unknown write fence mode");
                let mut fences = Vec::new();
                for host in hosts {
                    assert!(!host.is_empty(), "write fence host must not be empty");
                    let mut replica_config = handler_context.config.clone();
                    replica_config.db_host = host.to_owned();
                    let options = PgConnectOptions::from_str(&replica_config.database_dsn())
                        .expect("write fence database options")
                        .options([("default_transaction_read_only", "on")]);
                    // Avoid a separate cross-region ping before every WAL query.
                    // A dead connection fails the barrier closed instead.
                    let replica = PgPoolOptions::new()
                        .max_connections(2)
                        .test_before_acquire(false)
                        .connect_with(options)
                        .await
                        .expect("connect write fence standby");
                    let in_recovery: bool = sqlx::query_scalar("SELECT pg_is_in_recovery()")
                        .fetch_one(&replica)
                        .await
                        .expect("read write fence recovery state");
                    assert!(in_recovery, "write fence target must be a standby");
                    fences.push(ReadFence::new(
                        handler_context.db.clone(),
                        replica,
                        std::time::Duration::from_secs(30),
                    ));
                }
                Some(WriteFence::new(fences))
            }
        }
        _ => None,
    };
    // Standby reads require synchronous commits, a fresh read barrier, or a
    // response barrier on every writer (enforced by the deployment scripts).
    let read_context = match std::env::var("ISUREAL_DB_READ_HOST") {
        Ok(host) if !host.is_empty() => {
            let mode: String = sqlx::query_scalar("SHOW synchronous_commit")
                .fetch_one(&handler_context.db)
                .await
                .expect("read replication mode");
            let standbys: String = sqlx::query_scalar("SHOW synchronous_standby_names")
                .fetch_one(&handler_context.db)
                .await
                .expect("read synchronous standbys");
            let fenced = std::env::var("ISUREAL_READ_FENCE").is_ok_and(|value| value == "1");
            if fenced || write_fence.is_some() {
                assert!(
                    matches!(mode.as_str(), "local" | "on" | "remote_apply"),
                    "fenced reads require durable primary commits"
                );
            } else {
                assert_eq!(
                    mode, "remote_apply",
                    "unfenced local reads require remote_apply"
                );
                assert!(
                    matches!(standbys.as_str(), TWO_STANDBYS | THREE_STANDBYS),
                    "unfenced local reads require both regional standbys"
                );
            }
            let mut read_config = handler_context.config.clone();
            read_config.db_host = host;
            let options = PgConnectOptions::from_str(&read_config.database_dsn())
                .expect("read database options")
                .statement_cache_capacity(512)
                .options([("default_transaction_read_only", "on")]);
            let read_db = crate::db_pool::options(crate::db_pool::metadata_connections())
                .connect_with(options)
                .await
                .expect("connect local read database");
            let in_recovery: bool = sqlx::query_scalar("SELECT pg_is_in_recovery()")
                .fetch_one(&read_db)
                .await
                .expect("read recovery state");
            assert!(in_recovery, "local read database must be a standby");
            let slot: String = sqlx::query_scalar("SHOW primary_slot_name")
                .fetch_one(&read_db)
                .await
                .expect("read standby slot identity");
            assert!(
                fenced || matches!(slot.as_str(), "astra_europe" | "astra_america")
                    || (slot == "astra_asia" && standbys == THREE_STANDBYS
                        && (matches!(&write_fence, Some(WriteFence::Native(_)))
                            || mode == "remote_apply")),
                "a standby outside the regional write barrier requires a fresh read fence"
            );
            let read_fence = fenced.then(|| {
                ReadFence::new(
                    handler_context.db.clone(),
                    read_db.clone(),
                    std::time::Duration::from_secs(10),
                )
            });
            Arc::new(HandlerContext {
                image_store: handler_context.image_store.clone(),
                read_fence,
                db: read_db,
                config: handler_context.config.clone(),
                ip_bans: IpBanList::default(),
                notifications: NotificationHub::default(),
                notification_batch_writes,
            })
        }
        _ => handler_context.clone(),
    };
    let scope = std::env::var("ISUREAL_DB_READ_SCOPE").unwrap_or_else(|_| "all".into());
    assert!(
        matches!(scope.as_str(), "all" | "feed_search"),
        "unknown read scope"
    );
    let selective = scope == "feed_search";
    assert!(
        !selective
            || (handler_context.config.region == "asia"
                && !Arc::ptr_eq(&read_context, &handler_context)),
        "selective Asia reads require a local standby protected by read or write fences"
    );
    let general_reads = if selective {
        handler_context.clone()
    } else {
        read_context.clone()
    };
    let history = if std::env::var("ISUREAL_INITIAL_HISTORY").as_deref() == Ok("1") && WriteIngress::from_env().is_none() {
        let history = Arc::new(crate::initial_history::InitialHistory::load_with_export(&general_reads.db, &handler_context.config.region, std::env::var("ISUREAL_INITIAL_HISTORY_EXPORT").as_deref() == Ok("1")).await.expect("load initial history"));
        history.observe();
        Some(history)
    } else { None };
    let session_cache = crate::session_cache::SessionCacheCoordinator::from_env(
        handler_context.db.clone(), &general_reads.db, &handler_context.config.db_password,
    ).await;
    if session_cache.is_some() {
        assert!(matches!(&write_fence, Some(WriteFence::Native(_))), "session caching requires the native replica barrier");
    }
    let graph_fence = if selective {
        read_context.read_fence.clone()
    } else {
        None
    };
    let memory_connections: u32 = std::env::var("ISUREAL_MEMORY_POOL_CONNECTIONS").unwrap_or_else(|_| "0".into()).parse().expect("memory pool capacity");
    let memory_reads = crate::db_pool::memory_context(&general_reads, if WriteIngress::from_env().is_none() { memory_connections } else { 0 }).await.expect("connect memory read pool");
    let read_session = |auth_required| {
        from_fn_with_state((general_reads.clone(), auth_required), session_middleware)
    };
    let memory_session = || {
        from_fn_with_state((memory_reads.clone(), true), crate::session::memory_session_middleware)
    };
    let graph_session = |auth_required| {
        from_fn_with_state((read_context.clone(), auth_required), session_middleware)
    };
    let session = |auth_required| {
        from_fn_with_state((handler_context.clone(), auth_required), session_middleware)
    };
    let region = || from_fn_with_state(handler_context.clone(), region_middleware);
    let admin_session = |auth_required| {
        from_fn_with_state(
            (handler_context.clone(), auth_required),
            admin_session_middleware,
        )
    };

    #[rustfmt::skip]
    let server = Router::new()
        .route("/api/initialize", post(initialize_handler))
        .route("/api/auth/register", post(register_handler))
        .route("/api/auth/login", post(login_handler))
        .route("/api/auth/logout", post(logout_handler).route_layer(session(true)))
        .route("/api/users", get(search_users_handler).route_layer(region()).route_layer(graph_session(true)).route_layer(from_fn_with_state(graph_fence.clone(), read_fence_middleware)).with_state(read_context.clone()))
        .route("/api/users/{userId}", get(get_user_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/me/export", get(export_posts_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/me", get(get_me_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/me", patch(update_me_handler).route_layer(session(true)))
        .route("/api/me/profile-image", put(put_profile_image_handler).route_layer(session(true)))
        .route("/api/me/profile-image", delete(delete_profile_image_handler).route_layer(session(true)))
        .route("/api/me/account-deletion", post(delete_account_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/posts", post(create_post_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/posting-window/current", get(get_current_posting_window_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/posts/me/current", get(get_my_current_post_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/posts/{postId}", get(get_post_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/posts/{postId}", patch(update_post_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/posts/{postId}", delete(delete_post_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/feeds/{feed}", get(get_feed_handler).route_layer(region()).route_layer(graph_session(true)).route_layer(from_fn_with_state(graph_fence.clone(), read_fence_middleware)).with_state(read_context.clone()))
        .route("/api/memories", get(get_memories_handler).route_layer(region()).route_layer(memory_session()).with_state(memory_reads.clone()))
        .route("/api/memories/{date}", get(get_memories_by_date_handler).route_layer(region()).route_layer(memory_session()).with_state(memory_reads.clone()))
        .route("/api/posts/{postId}/comments", post(create_comment_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/posts/{postId}/comments", get(get_comments_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/comments/{commentId}", delete(delete_comment_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/posts/{postId}/realmojis", post(replace_real_moji_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/posts/{postId}/realmojis", get(get_real_mojis_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/realmojis/{realMojiId}", delete(delete_real_moji_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/images/{imageId}", get(get_image_handler).with_state(general_reads.clone()))
        .route("/api/notifications", get(get_notifications_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/friend-requests", post(create_friend_request_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/friend-requests/{requestId}/{action}", post(transition_friend_request_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/relationships/{kind}", get(get_relationships_handler).route_layer(region()).route_layer(read_session(true)).with_state(general_reads.clone()))
        .route("/api/friendships/{userId}", delete(delete_friendship_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/follows/{userId}", put(follow_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/follows/{userId}", delete(unfollow_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/blocks/{userId}", put(block_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/blocks/{userId}", delete(unblock_handler).route_layer(region()).route_layer(session(true)))
        .route("/api/admin/auth/login", post(admin_login_handler))
        .route("/api/admin/auth/logout", post(admin_logout_handler).route_layer(admin_session(true)))
        .route("/api/admin/users/{userId}/certification", put(certify_user_handler).route_layer(admin_session(true)))
        .route("/api/admin/users/{userId}/certification", delete(delete_user_certification_handler).route_layer(admin_session(true)))
        .route("/api/admin/posting-window", post(open_posting_window_handler).route_layer(admin_session(true)))
        .fallback(not_found_handler)
        .method_not_allowed_fallback(method_not_allowed_handler)
        .layer(
            ServiceBuilder::new()
                .layer(from_fn(run_to_completion))
                .layer(from_fn_with_state(action_log, action_log_middleware))
                .layer(from_fn_with_state(write_fence, write_fence_middleware))
                .layer(CatchPanicLayer::custom(recover))
                .layer(from_fn_with_state(general_reads.read_fence.clone(), read_fence_middleware))
                .layer(from_fn_with_state(general_reads.clone(), ip_ban_middleware)),
        )
        .with_state(handler_context.clone());

    let server = if let Some(history) = history { server.layer(axum::Extension(history)) } else { server };

    let server = if let Some(cache) = &session_cache {
        server.layer(crate::session_cache::layer(cache.clone()))
    } else { server };

    let server = if let Some(proxy) = crate::upload_proxy::UploadProxy::from_env(&general_reads) {
        server.layer(from_fn_with_state(
            proxy,
            crate::upload_proxy::upload_proxy_middleware,
        ))
    } else {
        server
    };

    let server = if let Some(auth) = crate::stored_upload::UploadAuthenticator::from_env() {
        server.layer(from_fn_with_state(auth, crate::stored_upload::authenticate))
    } else {
        server
    };
    let ingress = WriteIngress::from_env();
    let server = if ingress.is_none() {
        if let Some(receiver) = crate::image_cache_push::receiver_from_env() {
            server.merge(receiver)
        } else {
            server
        }
    } else {
        server
    };
    let server = if ingress.is_none() {
        if let Some(cache) = session_cache { server.merge(cache.receiver()) } else { server }
    } else { server };
    match ingress {
        Some(policy) => server.layer(from_fn_with_state(policy, write_ingress_middleware)),
        None => server,
    }
}

async fn bind_listener(address: &str, backlog: u32) -> io::Result<TcpListener> {
    let mut error = io::Error::new(io::ErrorKind::InvalidInput, "no listen addresses");
    for address in tokio::net::lookup_host(address).await? {
        let bind = || {
            let socket = if address.is_ipv4() {
                tokio::net::TcpSocket::new_v4()?
            } else {
                tokio::net::TcpSocket::new_v6()?
            };
            socket.set_reuseaddr(true)?;
            socket.bind(address)?;
            socket.listen(backlog)
        };
        match bind() {
            Ok(listener) => return Ok(listener),
            Err(err) => error = err,
        }
    }
    Err(error)
}

pub async fn start(server: Router, address: &str) -> io::Result<()> {
    let backlog = std::env::var("ISUREAL_LISTEN_BACKLOG")
        .unwrap_or_else(|_| "128".into())
        .parse::<u32>()
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidInput, error))?;
    if !(128..=4096).contains(&backlog) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "listen backlog out of range",
        ));
    }
    let listener = bind_listener(address, backlog).await?.tap_io(|tcp| {
        let _ = tcp.set_nodelay(true);
    });
    axum::serve(
        listener,
        server.into_make_service_with_connect_info::<SocketAddr>(),
    )
    .await
}

pub async fn run_to_completion(req: Request, next: Next) -> Response {
    match tokio::spawn(next.run(req)).await {
        Ok(response) => response,
        Err(err) => match err.try_into_panic() {
            Ok(panic) => std::panic::resume_unwind(panic),
            Err(_) => StatusCode::INTERNAL_SERVER_ERROR.into_response(),
        },
    }
}

async fn not_found_handler() -> Error {
    Error::Http(HttpError::new(StatusCode::NOT_FOUND))
}

async fn method_not_allowed_handler() -> Error {
    Error::Http(HttpError::new(StatusCode::METHOD_NOT_ALLOWED))
}

fn recover(err: Box<dyn Any + Send + 'static>) -> Response {
    let message = if let Some(message) = err.downcast_ref::<String>() {
        message.clone()
    } else if let Some(message) = err.downcast_ref::<&str>() {
        message.to_string()
    } else {
        "panic".to_owned()
    };
    Error::Internal(anyhow::anyhow!("[PANIC RECOVER] {message}")).into_response()
}

#[cfg(test)]
mod listen_tests {
    #[tokio::test]
    async fn configured_backlog_accepts_a_burst_before_the_application_polls() {
        let listener = super::bind_listener("127.0.0.1:0", 4096).await.unwrap();
        let address = listener.local_addr().unwrap();
        let clients = tokio::time::timeout(
            std::time::Duration::from_secs(2),
            futures_util::future::join_all(
                (0..256).map(|_| tokio::net::TcpStream::connect(address)),
            ),
        )
        .await
        .unwrap();
        assert!(clients.iter().all(Result::is_ok));
    }
}
