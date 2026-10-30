//! Startup snapshots of initial history. A fresh, single SQL statement checks
//! dirty owner/days and fetches current users before any cached data is served.
//! Reset only removes post-boundary records, which are never cached. The dirty
//! set is intentionally retained across reset; it can only cause SQL fallback.
use crate::{
    api_error::{Error, new_api_error},
    comment::Comment,
    comment_api::new_comment_response,
    memory_api::{GetMemoriesResponse, MemoryPostResponse},
    post::{POST_COLUMNS, Post, PostLocation},
    post_api::new_post_response,
    realmoji::RealMoji,
    realmoji_api::new_real_moji_response,
    user::{USER_COLUMNS, User},
};
use chrono::{DateTime, NaiveDate, Utc};
use futures_util::TryStreamExt;
use sqlx::{PgConnection, PgPool, Row};
use std::{
    collections::{HashMap, HashSet},
    sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    },
};
use uuid::Uuid;

struct HistoryPost {
    id: Uuid,
    window: Uuid,
    image: Uuid,
    caption: Box<str>,
    visibility: Box<str>,
    kind: Box<str>,
    locality: Option<Box<str>>,
    latitude: Option<f64>,
    longitude: Option<f64>,
    posted_at: DateTime<Utc>,
    deadline: DateTime<Utc>,
    date: NaiveDate,
    asset_key: Option<Box<str>>,
    asset_seen: bool,
    comments: Vec<HistoryComment>,
    reactions: Vec<HistoryReaction>,
}
struct HistoryComment {
    id: Uuid,
    author: Uuid,
    body: Box<str>,
    created_at: DateTime<Utc>,
}
struct HistoryReaction {
    id: Uuid,
    author: Uuid,
    image: Uuid,
    kind: Box<str>,
    created_at: DateTime<Utc>,
}
pub struct InitialHistory {
    epoch: String,
    authors: HashMap<String, Vec<HistoryPost>>,
    hits: AtomicU64,
    fallbacks: AtomicU64,
    export_enabled: bool,
    export_hits: AtomicU64,
    export_fallbacks: AtomicU64,
}

impl InitialHistory {
    pub async fn load(db: &PgPool, region: &str) -> Result<Self, Error> {
        Self::load_with_export(db, region, false).await
    }

    pub async fn load_with_export(
        db: &PgPool,
        region: &str,
        export_enabled: bool,
    ) -> Result<Self, Error> {
        let started = std::time::Instant::now();
        let mut tx = db.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
            .execute(&mut *tx)
            .await?;
        let epoch =
            sqlx::query_scalar("SELECT epoch FROM astra_initial_history_epoch WHERE singleton")
                .fetch_one(&mut *tx)
                .await?;
        let owners: Vec<String> =
            sqlx::query_scalar("SELECT id FROM initial_users WHERE region=$1")
                .bind(region)
                .fetch_all(&mut *tx)
                .await?;
        let mut authors: HashMap<String, Vec<HistoryPost>> =
            owners.into_iter().map(|id| (id, Vec::new())).collect();
        let mut by_post = HashMap::new();
        {
            let query = format!(
                "SELECT {POST_COLUMNS}, w.local_date FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id JOIN initial_users u ON u.id=p.author_id WHERE u.region=$1 AND p.deleted_at IS NULL AND p.created_at<=(SELECT created_at FROM initial_data_boundary) ORDER BY p.posted_at DESC,p.id DESC"
            );
            let mut rows = sqlx::query(&query).bind(region).fetch(&mut *tx);
            while let Some(row) = rows.try_next().await? {
                let p = <Post as sqlx::FromRow<_>>::from_row(&row)?;
                let id = Uuid::parse_str(&p.id).map_err(anyhow::Error::from)?;
                let posts = authors.get_mut(&p.author_id).expect("initial owner");
                by_post.insert(id, (p.author_id, posts.len()));
                posts.push(HistoryPost {
                    id,
                    window: Uuid::parse_str(&p.posting_window_id).map_err(anyhow::Error::from)?,
                    image: Uuid::parse_str(&p.image_id).map_err(anyhow::Error::from)?,
                    caption: p.caption.into(),
                    visibility: p.visibility.into(),
                    kind: p.post_location.kind.into(),
                    locality: p.post_location.locality.map(Into::into),
                    latitude: p.post_location.latitude,
                    longitude: p.post_location.longitude,
                    posted_at: p.posted_at,
                    deadline: p.on_time_deadline,
                    date: row.try_get("local_date")?,
                    asset_key: None,
                    asset_seen: false,
                    comments: Vec::new(),
                    reactions: Vec::new(),
                });
            }
        }
        {
            let mut rows=sqlx::query_as::<_,Comment>("SELECT c.* FROM comments c JOIN posts p ON p.id=c.post_id JOIN initial_users u ON u.id=p.author_id WHERE u.region=$1 AND c.deleted_at IS NULL AND c.created_at<=(SELECT created_at FROM initial_data_boundary) ORDER BY c.created_at,c.id").bind(region).fetch(&mut *tx);
            while let Some(c) = rows.try_next().await? {
                if let Some((owner, index)) =
                    by_post.get(&Uuid::parse_str(&c.post_id).map_err(anyhow::Error::from)?)
                {
                    authors.get_mut(owner).unwrap()[*index]
                        .comments
                        .push(HistoryComment {
                            id: Uuid::parse_str(&c.id).map_err(anyhow::Error::from)?,
                            author: Uuid::parse_str(&c.author_id).map_err(anyhow::Error::from)?,
                            body: c.body.into(),
                            created_at: c.created_at,
                        });
                }
            }
        }
        {
            let mut rows=sqlx::query_as::<_,RealMoji>("SELECT r.* FROM realmojis r JOIN posts p ON p.id=r.post_id JOIN initial_users u ON u.id=p.author_id WHERE u.region=$1 AND r.deleted_at IS NULL AND r.replaced_at IS NULL AND r.created_at<=(SELECT created_at FROM initial_data_boundary) ORDER BY r.created_at DESC,r.id DESC").bind(region).fetch(&mut *tx);
            while let Some(r) = rows.try_next().await? {
                if let Some((owner, index)) =
                    by_post.get(&Uuid::parse_str(&r.post_id).map_err(anyhow::Error::from)?)
                {
                    authors.get_mut(owner).unwrap()[*index]
                        .reactions
                        .push(HistoryReaction {
                            id: Uuid::parse_str(&r.id).map_err(anyhow::Error::from)?,
                            author: Uuid::parse_str(&r.reactor_id).map_err(anyhow::Error::from)?,
                            image: Uuid::parse_str(&r.image_id).map_err(anyhow::Error::from)?,
                            kind: r.reaction_type.into(),
                            created_at: r.created_at,
                        });
                }
            }
        }
        if export_enabled {
            let mut by_image = HashMap::new();
            for (owner, posts) in &authors {
                for (index, post) in posts.iter().enumerate() {
                    by_image
                        .entry(post.image)
                        .and_modify(|entry| *entry = None)
                        .or_insert(Some((owner.clone(), index)));
                }
            }
            let mut rows = sqlx::query("SELECT i.id,i.asset_key,i.created_at<=(SELECT created_at FROM initial_data_boundary) AS initial FROM image_objects i JOIN posts p ON p.image_id=i.id JOIN initial_users u ON u.id=p.author_id WHERE u.region=$1 AND p.deleted_at IS NULL AND p.created_at<=(SELECT created_at FROM initial_data_boundary)").bind(region).fetch(&mut *tx);
            while let Some(row) = rows.try_next().await? {
                let id =
                    Uuid::parse_str(row.try_get::<&str, _>("id")?).map_err(anyhow::Error::from)?;
                if let Some(Some((owner, index))) = by_image.get(&id) {
                    let post = &mut authors.get_mut(owner).unwrap()[*index];
                    post.asset_key = if post.asset_seen || !row.try_get::<bool, _>("initial")? {
                        None
                    } else {
                        row.try_get::<Option<String>, _>("asset_key")?
                            .map(Into::into)
                    };
                    post.asset_seen = true;
                }
            }
        }
        tx.commit().await?;
        let posts: usize = authors.values().map(Vec::len).sum();
        let comments: usize = authors.values().flatten().map(|p| p.comments.len()).sum();
        let reactions: usize = authors.values().flatten().map(|p| p.reactions.len()).sum();
        eprintln!(
            "{}",
            serde_json::json!({"metric":"initial_history_loaded", "region":region,"posts":posts,"comments":comments,"reactions":reactions,"elapsed_ms":started.elapsed().as_millis()})
        );
        Ok(Self {
            epoch,
            authors,
            hits: AtomicU64::new(0),
            fallbacks: AtomicU64::new(0),
            export_enabled,
            export_hits: AtomicU64::new(0),
            export_fallbacks: AtomicU64::new(0),
        })
    }

    pub fn metrics(&self) -> serde_json::Value {
        serde_json::json!({"metric":"initial_history", "hits":self.hits.load(Ordering::Relaxed),"fallbacks":self.fallbacks.load(Ordering::Relaxed),"export_hits":self.export_hits.load(Ordering::Relaxed),"export_fallbacks":self.export_fallbacks.load(Ordering::Relaxed)})
    }
    pub fn observe(self: &Arc<Self>) {
        let weak = Arc::downgrade(self);
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(std::time::Duration::from_secs(10)).await;
                let Some(cache) = weak.upgrade() else { break };
                eprintln!("{}", cache.metrics());
            }
        });
    }

    /// Reject unsupported owners before acquiring a database connection.
    pub fn can_export(&self, owner: &str) -> bool {
        if !self.export_enabled {
            return false;
        }
        let available = self
            .authors
            .get(owner)
            .is_some_and(|posts| posts.iter().all(|p| p.asset_key.is_some()));
        if !available {
            self.export_fallbacks.fetch_add(1, Ordering::Relaxed);
        }
        available
    }

    /// Return the current author and unchanged initial post metadata. The
    /// caller must still read post-boundary posts freshly and merge their rows.
    pub async fn export(
        &self,
        db: &mut PgConnection,
        owner: &str,
    ) -> Result<Option<(User, Vec<(Post, String)>)>, Error> {
        if !self.can_export(owner) {
            return Ok(None);
        }
        let posts = &self.authors[owner];
        let query = format!(
            "SELECT {} FROM users u WHERE u.id=$1 AND u.status='active' AND EXISTS(SELECT 1 FROM astra_initial_history_epoch WHERE singleton AND epoch=$2) AND NOT EXISTS(SELECT 1 FROM astra_initial_export_dirty WHERE owner_id=$1)",
            USER_COLUMNS
                .iter()
                .map(|c| format!("u.{c}"))
                .collect::<Vec<_>>()
                .join(",")
        );
        let Some(mut author) = sqlx::query_as::<_, User>(&query)
            .bind(owner)
            .bind(&self.epoch)
            .fetch_optional(&mut *db)
            .await?
        else {
            self.export_fallbacks.fetch_add(1, Ordering::Relaxed);
            return Ok(None);
        };
        author.set_image_key()?;
        self.export_hits.fetch_add(1, Ordering::Relaxed);
        Ok(Some((
            author,
            posts
                .iter()
                .rev()
                .map(|p| (p.post(owner), p.asset_key.as_ref().unwrap().to_string()))
                .collect(),
        )))
    }

    pub async fn memories(
        &self,
        db: &mut PgConnection,
        owner: &str,
        start: NaiveDate,
        end: NaiveDate,
        before: Option<DateTime<Utc>>,
        before_id: &str,
    ) -> Result<Option<GetMemoriesResponse>, Error> {
        if before.is_none() != before_id.is_empty() {
            return Err(new_api_error(
                axum::http::StatusCode::BAD_REQUEST,
                "invalid_pagination",
            ));
        }
        let Some(posts) = self.authors.get(owner) else {
            self.fallbacks.fetch_add(1, Ordering::Relaxed);
            return Ok(None);
        };
        let before_uuid = if before.is_some() {
            match Uuid::parse_str(before_id) {
                Ok(id) if id.to_string() == before_id => Some(id),
                _ => return Ok(None),
            }
        } else {
            None
        };
        let page: Vec<_> = posts
            .iter()
            .filter(|p| {
                p.date >= start
                    && p.date < end
                    && before.is_none_or(|at| {
                        p.posted_at < at || (p.posted_at == at && Some(p.id) < before_uuid)
                    })
            })
            .take(51)
            .collect();
        let mut users = HashSet::from([owner.to_owned()]);
        for p in page.iter().take(50) {
            for c in &p.comments {
                users.insert(c.author.to_string());
            }
            for r in &p.reactions {
                users.insert(r.author.to_string());
            }
        }
        let query = format!(
            "WITH valid AS MATERIALIZED (SELECT epoch=$1 AND NOT EXISTS(SELECT 1 FROM astra_initial_history_dirty WHERE owner_id=$2 AND local_date >= $3 AND local_date < $4) AS valid FROM astra_initial_history_epoch WHERE singleton) SELECT valid.valid, {} FROM valid LEFT JOIN users u ON valid.valid AND u.id=ANY($5::text[]) AND u.status='active'",
            USER_COLUMNS
                .iter()
                .map(|c| format!("u.{c}"))
                .collect::<Vec<_>>()
                .join(",")
        );
        let rows = sqlx::query(&query)
            .bind(&self.epoch)
            .bind(owner)
            .bind(start)
            .bind(end)
            .bind(users.into_iter().collect::<Vec<_>>())
            .fetch_all(&mut *db)
            .await?;
        if rows.is_empty() || !rows[0].try_get::<bool, _>("valid")? {
            self.fallbacks.fetch_add(1, Ordering::Relaxed);
            return Ok(None);
        }
        let mut users = HashMap::new();
        for row in rows {
            if let Some(id) = row.try_get::<Option<String>, _>("id")? {
                let mut u = <User as sqlx::FromRow<_>>::from_row(&row)?;
                u.set_image_key()?;
                users.insert(id, u);
            }
        }
        self.hits.fetch_add(1, Ordering::Relaxed);
        let Some(author) = users.get(owner) else {
            return Ok(Some(GetMemoriesResponse::default()));
        };
        let mut response = GetMemoriesResponse {
            has_more: page.len() > 50,
            items: Vec::with_capacity(page.len().min(50)),
        };
        for p in page.into_iter().take(50) {
            let post = p.post(owner);
            let mut item = MemoryPostResponse {
                create_post_response: new_post_response(&post, author),
                comments: Vec::new(),
                realmojis: Vec::new(),
            };
            for c in &p.comments {
                if let Some(user) = users.get(&c.author.to_string()) {
                    item.comments.push(new_comment_response(
                        &Comment {
                            id: c.id.to_string(),
                            post_id: post.id.clone(),
                            author_id: user.id.clone(),
                            body: c.body.to_string(),
                            created_at: c.created_at,
                            deleted_at: None,
                        },
                        user,
                    ));
                    if item.comments.len() == 50 {
                        break;
                    }
                }
            }
            for r in &p.reactions {
                if let Some(user) = users.get(&r.author.to_string()) {
                    item.realmojis.push(new_real_moji_response(
                        &RealMoji {
                            id: r.id.to_string(),
                            post_id: post.id.clone(),
                            reactor_id: user.id.clone(),
                            image_id: r.image.to_string(),
                            reaction_type: r.kind.to_string(),
                            created_at: r.created_at,
                            replaced_at: None,
                            deleted_at: None,
                        },
                        user,
                    ));
                    if item.realmojis.len() == 50 {
                        break;
                    }
                }
            }
            response.items.push(item);
        }
        Ok(Some(response))
    }
}

impl HistoryPost {
    fn post(&self, owner: &str) -> Post {
        Post {
            id: self.id.to_string(),
            author_id: owner.into(),
            posting_window_id: self.window.to_string(),
            image_id: self.image.to_string(),
            caption: self.caption.to_string(),
            visibility: self.visibility.to_string(),
            post_location: PostLocation {
                kind: self.kind.to_string(),
                latitude: self.latitude,
                longitude: self.longitude,
                locality: self.locality.as_deref().map(str::to_owned),
            },
            posted_at: self.posted_at,
            on_time_deadline: self.deadline,
            window_ended_at: None,
        }
    }
}
