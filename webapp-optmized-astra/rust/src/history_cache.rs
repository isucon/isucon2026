//! Cache owned records, validating a transactionally replicated change ledger.
//! User state and signing keys are deliberately read fresh when rendering.
use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, LazyLock, Mutex};

use chrono::{Datelike, Months, NaiveDate};
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
    pub(crate) fn contains(&self, key: &str) -> bool {
        self.entries.contains_key(key)
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
pub(crate) async fn load(db: &PgPool, owner: &str, date: NaiveDate) -> Result<Arc<History>, Error> {
    let month = date.with_day(1).expect("valid month start");
    let key = format!("{owner}:{month}");
    let present = HISTORIES.lock().unwrap().contains(&key);
    if present {
        let version = revision(&mut *db.acquire().await?, owner).await?;
        if let Some(history) = HISTORIES.lock().unwrap().get(&key, &version) {
            return Ok(history);
        }
    }
    // One statement gives records and revision the same MVCC snapshot without
    // BEGIN/COMMIT or a separate admission queue. The SQL pool bounds loaders.
    // LEFT JOIN retains the revision row even for owners without any posts.
    let rows = sqlx::query(&format!(r#"
        WITH version AS MATERIALIZED (
          SELECT epoch,
            (SELECT count(*) FROM astra_history_changes WHERE owner_id='') AS global_changes,
            (SELECT count(*) FROM astra_history_changes WHERE owner_id=$1) AS owner_changes
          FROM astra_history_epoch WHERE singleton
        ), owned AS MATERIALIZED (
          SELECT {POST_COLUMNS}, w.local_date FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id
          WHERE p.author_id=$1 AND p.deleted_at IS NULL AND w.local_date >= $2 AND w.local_date < $3
        )
        SELECT version.*, owned.*,
          coalesce((SELECT json_agg(c ORDER BY c.created_at,c.id)::text FROM (
            SELECT id,post_id,author_id,body,created_at,deleted_at FROM comments
            WHERE post_id=owned.id AND deleted_at IS NULL
          ) c),'[]') AS comments_json,
          coalesce((SELECT json_agg(r ORDER BY r.created_at DESC,r.id DESC)::text FROM (
            SELECT id,post_id,reactor_id,image_id,reaction_type,created_at,replaced_at,deleted_at FROM realmojis
            WHERE post_id=owned.id AND replaced_at IS NULL AND deleted_at IS NULL
          ) r),'[]') AS reactions_json
        FROM version LEFT JOIN owned ON true ORDER BY owned.posted_at DESC,owned.id DESC
    "#)).bind(owner).bind(month).bind(month + Months::new(1)).fetch_all(db).await?;
    let version = Revision::from_row(rows.first().ok_or(sqlx::Error::RowNotFound)?)?;
    let mut posts = Vec::new();
    let mut comments = Vec::<Comment>::new();
    let mut reactions = Vec::<RealMoji>::new();
    for row in rows {
        if row.try_get::<Option<String>, _>("id")?.is_none() {
            continue;
        }
        posts.push(HistoryPost::from_row(&row)?);
        comments.extend(serde_json::from_str::<Vec<Comment>>(
            row.try_get("comments_json")?,
        )?);
        reactions.extend(serde_json::from_str::<Vec<RealMoji>>(
            row.try_get("reactions_json")?,
        )?);
    }
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
        .insert(&key, version, history.clone(), bytes);
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
