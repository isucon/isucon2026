use std::sync::{Arc, Mutex};

use anyhow::Context as _;
use axum::extract::{Request, State};
use axum::http::{HeaderMap, Method, StatusCode, header};
use axum::middleware::Next;
use axum::response::Response;
use chrono::{DateTime, Utc};
use sha2::{Digest, Sha256};
use sqlx::{Executor, PgConnection, PgPool, Postgres, pool::PoolConnection};
use uuid::Uuid;

use crate::api_error::{Error, new_api_error};
use crate::logger;
use crate::read_probe::ReadProbe;
use crate::server::HandlerContext;

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct Session {
    #[sqlx(default)]
    pub region: String,
    #[sqlx(skip)]
    pub token: String,
    pub user_id: String,
    pub token_hash: String,
    pub created_at: DateTime<Utc>,
}

pub fn get_session(req: &Request) -> Option<&Session> {
    req.extensions().get::<Session>()
}

fn token_hash(headers: &HeaderMap) -> Result<Option<String>, Error> {
    let auth_header = headers
        .get(header::AUTHORIZATION)
        .map(|value| String::from_utf8_lossy(value.as_bytes()).into_owned())
        .unwrap_or_default();
    if auth_header.is_empty() {
        return Ok(None);
    }
    let Some(token) = auth_header.strip_prefix("Bearer ") else {
        logger::warn("Invalid authorization header format(not started with Bearer)");
        return Err(new_api_error(StatusCode::UNAUTHORIZED, "invalid_session"));
    };

    Ok(Some(hex::encode(Sha256::digest(token.as_bytes()))))
}

async fn get_session_by_hash<'c>(
    token_hash: &str,
    db: impl Executor<'c, Database = Postgres>,
) -> Result<Option<Session>, Error> {
    let session: Option<Session> = sqlx::query_as(
        r#"
		SELECT s.user_id, s.token_hash, s.created_at, u.region
		FROM login_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.active = true AND u.status = 'active'
	"#,
    )
    .bind(token_hash)
    .fetch_optional(db)
    .await
    .context("failed to get session")?;
    Ok(session)
}

/// A connection belongs to one request and is consumed once by its memory handler.
/// The Arc only satisfies Axum's Clone requirement for request extensions.
#[derive(Clone)]
pub struct SessionConnection(Arc<Mutex<Option<PoolConnection<Postgres>>>>);

impl SessionConnection {
    pub fn take(&self) -> Option<PoolConnection<Postgres>> {
        self.0.lock().unwrap().take()
    }
}

pub async fn session_middleware(
    State(context): State<(Arc<HandlerContext>, bool)>,
    req: Request,
    next: Next,
) -> Result<Response, Error> {
    authenticate_request(context, req, next, false).await
}

pub async fn memory_session_middleware(
    State(context): State<(Arc<HandlerContext>, bool)>,
    req: Request,
    next: Next,
) -> Result<Response, Error> {
    authenticate_request(context, req, next, true).await
}

async fn authenticate_request(
    (handler_context, auth_required): (Arc<HandlerContext>, bool),
    mut req: Request,
    next: Next,
    reuse_connection: bool,
) -> Result<Response, Error> {
    let mut probe = ReadProbe::sample(req.uri().path());
    let started = std::time::Instant::now();
    let cache = if matches!(*req.method(), Method::GET | Method::HEAD) {
        crate::session_cache::coordinator(&req)
    } else {
        None
    };
    let token_hash = token_hash(req.headers())?;
    let (cached, generation) = match (&cache, &token_hash) {
        (Some(cache), Some(hash)) => cache.cache.lookup(hash),
        _ => (None, 0),
    };
    let session = if let Some(session) = cached {
        Some(session)
    } else if let Some(hash) = token_hash {
        let session = if reuse_connection {
            let mut db = handler_context.db.acquire().await?;
            let session = get_session_by_hash(&hash, &mut *db).await?;
            req.extensions_mut()
                .insert(SessionConnection(Arc::new(Mutex::new(Some(db)))));
            session
        } else {
            get_session_by_hash(&hash, &handler_context.db).await?
        };
        if let (Some(cache), Some(session)) = (&cache, &session) {
            cache.cache.insert(session.clone(), generation, started);
        }
        session
    } else {
        None
    };
    ReadProbe::mark(&mut probe, "auth");
    if let Some(probe) = probe {
        req.extensions_mut().insert(probe);
    }
    if session.is_none() && auth_required {
        return Err(new_api_error(StatusCode::UNAUTHORIZED, "invalid_session"));
    }
    if let Some(session) = &session {
        req.extensions_mut().insert(session.clone());
    }
    let mut response = next.run(req).await;
    if let Some(session) = session {
        response.extensions_mut().insert(session);
    }
    Ok(response)
}

pub async fn invalidate_login_session(
    db: &PgPool,
    user_id: &str,
    token_hash: &str,
) -> Result<(), Error> {
    let mut tx = db.begin().await.context("failed to begin transaction")?;

    let _locked_user_id: String =
        sqlx::query_scalar("SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE")
            .bind(user_id)
            .fetch_one(&mut *tx)
            .await
            .context("failed to lock user")?;

    sqlx::query(
        r#"
		UPDATE login_sessions
		SET active = false, invalidated_at = clock_timestamp(), reason = 'logout'
		WHERE user_id = $1 AND active = true AND token_hash = $2
	"#,
    )
    .bind(user_id)
    .bind(token_hash)
    .execute(&mut *tx)
    .await
    .context("failed to invalidate sessions")?;

    tx.commit().await.context("failed to commit transaction")?;

    Ok(())
}

// 2026-09-03: リファクタリング後にこの関数の利用がないようです。消すのはいかがでしょうか
pub async fn create_login_session(db: &PgPool, user_id: &str) -> Result<Session, Error> {
    let mut tx = db.begin().await.context("failed to begin transaction")?;
    let session = create_login_session_tx(&mut tx, user_id).await?;
    tx.commit().await.context("failed to commit transaction")?;
    Ok(session)
}

pub async fn create_login_session_tx(
    tx: &mut PgConnection,
    user_id: &str,
) -> Result<Session, Error> {
    let _locked_user_id: String = sqlx::query_scalar(
        "SELECT id FROM users WHERE id = $1 AND status = 'active' FOR NO KEY UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut *tx)
    .await
    .context("failed to lock user")?;
    create_login_session_for_locked_user_tx(tx, user_id).await
}

/// The caller must hold the active user's row lock until this transaction ends.
/// Login already acquired it while checking the current credentials and status.
pub(crate) async fn create_login_session_for_locked_user_tx(
    tx: &mut PgConnection,
    user_id: &str,
) -> Result<Session, Error> {
    let random_token = Uuid::new_v4().to_string();
    let token_hash = hex::encode(Sha256::digest(random_token.as_bytes()));
    sqlx::query(
        r#"
  UPDATE login_sessions
  SET active = false, invalidated_at = clock_timestamp(), reason = 'replaced'
  WHERE user_id = $1 AND active = true
 "#,
    )
    .bind(user_id)
    .execute(&mut *tx)
    .await
    .context("failed to invalidate sessions")?;
    let session_id = Uuid::new_v4().to_string();
    let mut session: Session = sqlx::query_as(
        r#"
  INSERT INTO login_sessions (id, user_id, token_hash)
  VALUES ($1, $2, $3)
  RETURNING user_id, token_hash, created_at
 "#,
    )
    .bind(&session_id)
    .bind(user_id)
    .bind(&token_hash)
    .fetch_one(&mut *tx)
    .await
    .context("failed to insert session")?;
    session.token = random_token;
    Ok(session)
}
