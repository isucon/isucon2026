//! Fresh authentication for requests already waiting for a database connection.
//! No authentication result survives a batch. Requests arriving after its SQL
//! starts are checked by a later statement, so logout cannot reuse an old result.
use std::collections::HashMap;
use std::sync::{Arc, OnceLock};

use anyhow::Context as _;
use sqlx::PgPool;
use tokio::sync::{mpsc, oneshot};

use super::Session;
use crate::api_error::Error;

const BATCH_LIMIT: usize = 128;
const QUEUE_LIMIT: usize = 1024;
type LookupResult = Result<Option<Session>, Arc<str>>;

struct Lookup {
    token_hash: String,
    response: oneshot::Sender<LookupResult>,
}

#[derive(Default)]
pub struct SessionLookups {
    sender: OnceLock<mpsc::Sender<Lookup>>,
}

impl SessionLookups {
    pub(crate) async fn lookup(
        &self,
        db: &PgPool,
        token_hash: String,
    ) -> Result<Option<Session>, Error> {
        let sender = self.sender.get_or_init(|| {
            let (sender, receiver) = mpsc::channel(QUEUE_LIMIT);
            tokio::spawn(run(db.clone(), receiver));
            sender
        });
        let (response, result) = oneshot::channel();
        sender
            .send(Lookup {
                token_hash,
                response,
            })
            .await
            .map_err(|_| anyhow::anyhow!("session lookup worker stopped"))?;
        result
            .await
            .context("session lookup worker stopped")?
            .map_err(|error| anyhow::anyhow!("failed to get session: {error}").into())
    }
}

async fn run(db: PgPool, mut receiver: mpsc::Receiver<Lookup>) {
    while let Some(first) = receiver.recv().await {
        // Acquire before draining: requests accumulated during pool contention
        // can share this fresh statement without an artificial batching delay.
        let mut connection = match db.acquire().await {
            Ok(connection) => connection,
            Err(error) => {
                let _ = first.response.send(Err(error.to_string().into()));
                continue;
            }
        };
        let mut batch = Vec::with_capacity(BATCH_LIMIT);
        batch.push(first);
        while batch.len() < BATCH_LIMIT {
            match receiver.try_recv() {
                Ok(request) => batch.push(request),
                Err(_) => break,
            }
        }
        let mut hashes: Vec<_> = batch.iter().map(|r| r.token_hash.as_str()).collect();
        hashes.sort_unstable();
        hashes.dedup();
        let rows: Result<Vec<Session>, _> = sqlx::query_as(
            r#"
            SELECT s.user_id,s.token_hash,s.created_at,u.region
            FROM unnest($1::text[]) requested(token_hash)
            JOIN LATERAL (
                SELECT user_id,token_hash,created_at FROM login_sessions
                WHERE token_hash=requested.token_hash AND active=true OFFSET 0
            ) s ON true
            JOIN LATERAL (
                SELECT region FROM users
                WHERE id=s.user_id AND status='active' OFFSET 0
            ) u ON true
        "#,
        )
        .bind(&hashes)
        .fetch_all(&mut *connection)
        .await;
        drop(connection);
        match rows {
            Ok(rows) => {
                let sessions: HashMap<_, _> = rows
                    .into_iter()
                    .map(|session| (session.token_hash.clone(), session))
                    .collect();
                for request in batch {
                    let result = sessions.get(&request.token_hash).cloned();
                    let _ = request.response.send(Ok(result));
                }
            }
            Err(error) => {
                let error: Arc<str> = error.to_string().into();
                for request in batch {
                    let _ = request.response.send(Err(error.clone()));
                }
            }
        }
    }
}

#[cfg(test)]
#[path = "session_batch_tests.rs"]
mod tests;
