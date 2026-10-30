//! Cache owned records, validating a transactionally replicated change ledger.
//! User state and signing keys are deliberately read fresh when rendering.
use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, LazyLock, Mutex};

use chrono::NaiveDate;
use sqlx::{FromRow, PgConnection, PgPool, Row};

use crate::Error;
use crate::comment::Comment;
use crate::post::{POST_COLUMNS, Post};
use crate::realmoji::RealMoji;

#[derive(Clone, Debug, PartialEq, Eq, sqlx::FromRow)]
pub(crate) struct Revision {
    epoch: String,
    global_changes: i64,
    owner_changes: i64,
}

pub(crate) async fn revision(db: &mut PgConnection, owner: &str) -> Result<Revision, Error> {
    Ok(sqlx::query_as(
        "SELECT epoch, \
        (SELECT count(*) FROM astra_history_changes WHERE owner_id='') AS global_changes, \
        (SELECT count(*) FROM astra_history_changes WHERE owner_id=$1) AS owner_changes \
        FROM astra_history_epoch WHERE singleton",
    )
    .bind(owner)
    .fetch_one(db)
    .await?)
}

struct Entry<T> {
    revision: Revision,
    value: Arc<T>,
    bytes: usize,
}

pub(crate) struct Cache<T> {
    entries: HashMap<String, Entry<T>>,
    order: VecDeque<String>,
    bytes: usize,
    limit: usize,
}

impl<T> Cache<T> {
    pub(crate) fn new(limit: usize) -> Self {
        Self {
            entries: HashMap::new(),
            order: VecDeque::new(),
            bytes: 0,
            limit,
        }
    }
    pub(crate) fn get(&self, owner: &str, revision: &Revision) -> Option<Arc<T>> {
        self.entries
            .get(owner)
            .filter(|e| &e.revision == revision)
            .map(|e| e.value.clone())
    }
    pub(crate) fn insert(&mut self, owner: &str, revision: Revision, value: Arc<T>, bytes: usize) {
        if let Some(old) = self.entries.remove(owner) {
            self.bytes -= old.bytes;
            self.order.retain(|key| key != owner);
        }
        if bytes > self.limit {
            return;
        }
        while self.bytes + bytes > self.limit || self.entries.len() >= 2048 {
            let Some(key) = self.order.pop_front() else {
                break;
            };
            if let Some(old) = self.entries.remove(&key) {
                self.bytes -= old.bytes;
            }
        }
        self.order.push_back(owner.to_owned());
        self.bytes += bytes;
        self.entries.insert(
            owner.to_owned(),
            Entry {
                revision,
                value,
                bytes,
            },
        );
    }
}

pub(crate) struct HistoryPost {
    pub post: Post,
    pub date: NaiveDate,
}
impl<'r> FromRow<'r, sqlx::postgres::PgRow> for HistoryPost {
    fn from_row(row: &'r sqlx::postgres::PgRow) -> sqlx::Result<Self> {
        Ok(Self {
            post: Post::from_row(row)?,
            date: row.try_get("local_date")?,
        })
    }
}
pub(crate) struct History {
    pub posts: Vec<HistoryPost>,
    pub comments: HashMap<String, Vec<Comment>>,
    pub reactions: HashMap<String, Vec<RealMoji>>,
}

static HISTORIES: LazyLock<Mutex<Cache<History>>> =
    LazyLock::new(|| Mutex::new(Cache::new(96 * 1024 * 1024)));
static LOADS: tokio::sync::Semaphore = tokio::sync::Semaphore::const_new(4);

pub(crate) async fn load(db: &PgPool, owner: &str) -> Result<Arc<History>, Error> {
    let version = revision(&mut *db.acquire().await?, owner).await?;
    if let Some(history) = HISTORIES.lock().unwrap().get(owner, &version) {
        return Ok(history);
    }
    let _permit = LOADS.acquire().await.expect("history load semaphore");
    // Capture revision and all records in one snapshot. A concurrent writer's
    // records must never be inserted under an older or newer revision.
    let mut tx = db
        .begin_with("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .await?;
    let version = revision(&mut tx, owner).await?;
    let existing = HISTORIES.lock().unwrap().get(owner, &version);
    if let Some(history) = existing {
        tx.commit().await?;
        return Ok(history);
    }
    let posts: Vec<HistoryPost> = sqlx::query_as(&format!(
        "SELECT {POST_COLUMNS}, w.local_date FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id \
         WHERE p.author_id=$1 AND p.deleted_at IS NULL ORDER BY p.posted_at DESC,p.id DESC"))
        .bind(owner).fetch_all(&mut *tx).await?;
    let comments: Vec<Comment> = sqlx::query_as("SELECT c.id,c.post_id,c.author_id,c.body,c.created_at,c.deleted_at \
        FROM posts p JOIN comments c ON c.post_id=p.id WHERE p.author_id=$1 AND p.deleted_at IS NULL \
        AND c.deleted_at IS NULL ORDER BY c.created_at,c.id")
        .bind(owner).fetch_all(&mut *tx).await?;
    let reactions: Vec<RealMoji> = sqlx::query_as("SELECT r.id,r.post_id,r.reactor_id,r.image_id,r.reaction_type,r.created_at,r.replaced_at,r.deleted_at \
        FROM posts p JOIN realmojis r ON r.post_id=p.id WHERE p.author_id=$1 AND p.deleted_at IS NULL \
        AND r.replaced_at IS NULL AND r.deleted_at IS NULL ORDER BY r.created_at DESC,r.id DESC")
        .bind(owner).fetch_all(&mut *tx).await?;
    tx.commit().await?;
    // Include capacities/keys/containers conservatively, and reject oversized
    // entries. In-flight readers may temporarily retain evicted Arc values.
    let bytes = 1024
        + posts
            .iter()
            .map(|p| post_bytes(&p.post) + 64)
            .sum::<usize>()
        + comments
            .iter()
            .map(|c| {
                512 + c.body.capacity()
                    + c.id.capacity()
                    + c.post_id.capacity()
                    + c.author_id.capacity()
            })
            .sum::<usize>()
        + reactions
            .iter()
            .map(|r| {
                640 + r.id.capacity()
                    + r.post_id.capacity()
                    + r.reactor_id.capacity()
                    + r.image_id.capacity()
                    + r.reaction_type.capacity()
            })
            .sum::<usize>();
    let mut history = History {
        posts,
        comments: HashMap::new(),
        reactions: HashMap::new(),
    };
    for comment in comments {
        history
            .comments
            .entry(comment.post_id.clone())
            .or_default()
            .push(comment);
    }
    for reaction in reactions {
        history
            .reactions
            .entry(reaction.post_id.clone())
            .or_default()
            .push(reaction);
    }
    let history = Arc::new(history);
    HISTORIES
        .lock()
        .unwrap()
        .insert(owner, version, history.clone(), bytes);
    Ok(history)
}

pub(crate) fn post_bytes(p: &Post) -> usize {
    512 + p.id.capacity()
        + p.author_id.capacity()
        + p.posting_window_id.capacity()
        + p.image_id.capacity()
        + p.caption.capacity()
        + p.visibility.capacity()
        + p.post_location.kind.capacity()
        + p.post_location
            .locality
            .as_ref()
            .map_or(0, String::capacity)
}

#[cfg(test)]
#[path = "history_cache_tests.rs"]
mod tests;
