//! Initial post-image metadata guarded by fresh user and transactional dirty reads.
//! Image bytes stay in the existing immutable store. Changed owners fall back to
//! the complete image query, including alternative profile/reaction references.
use chrono::{DateTime, Utc};
use futures_util::TryStreamExt;
use sqlx::{Executor, PgPool, Postgres, Row};
use std::{
    collections::HashMap,
    sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    },
};
use uuid::Uuid;

struct Source {
    owner: Arc<str>,
    media_type: Arc<str>,
    asset_key: Box<str>,
    created_at: DateTime<Utc>,
}

pub struct InitialPostImages {
    reads: Option<PgPool>,
    epoch: String,
    images: HashMap<Uuid, Option<Source>>,
    hits: AtomicU64,
    fallbacks: AtomicU64,
}

pub struct ResolvedImage<'a> {
    pub(crate) owner: &'a str,
    pub(crate) media_type: &'a str,
    pub(crate) asset_key: &'a str,
    pub(crate) created_at: DateTime<Utc>,
    pub(crate) secret_key_hash: String,
}

impl InitialPostImages {
    pub async fn load_with_pool(
        db: &PgPool,
        region: &str,
        connections: u32,
    ) -> Result<Self, sqlx::Error> {
        assert!(
            connections <= 16,
            "initial image pool capacity must be 0–16"
        );
        let mut cache = Self::load(db, region).await?;
        if connections != 0 {
            cache.reads = Some(
                crate::db_pool::options(connections)
                    .min_connections(connections)
                    .connect_with(
                        (*db.connect_options())
                            .clone()
                            .application_name("astra_initial_images"),
                    )
                    .await?,
            );
        }
        Ok(cache)
    }

    pub(crate) fn read_pool<'a>(&'a self, fallback: &'a PgPool) -> &'a PgPool {
        self.reads.as_ref().unwrap_or(fallback)
    }

    pub async fn load(db: &PgPool, region: &str) -> Result<Self, sqlx::Error> {
        let started = std::time::Instant::now();
        let mut tx = db
            .begin_with("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .await?;
        let epoch =
            sqlx::query_scalar("SELECT epoch FROM astra_initial_history_epoch WHERE singleton")
                .fetch_one(&mut *tx)
                .await?;
        let mut images = HashMap::new();
        let mut owners: HashMap<String, Arc<str>> = HashMap::new();
        let mut media: HashMap<String, Arc<str>> = HashMap::new();
        {
            let mut rows = sqlx::query("SELECT i.id,i.owner_user_id,i.media_type,i.asset_key,i.created_at,p.author_id,i.created_at<=(SELECT created_at FROM initial_data_boundary) AS initial FROM image_objects i JOIN posts p ON p.image_id=i.id JOIN initial_users u ON u.id=p.author_id WHERE u.region=$1 AND p.deleted_at IS NULL AND p.created_at<=(SELECT created_at FROM initial_data_boundary)")
                .bind(region).fetch(&mut *tx);
            while let Some(row) = rows.try_next().await? {
                let raw: &str = row.try_get("id")?;
                let Ok(id) = Uuid::parse_str(raw) else {
                    continue;
                };
                if id.to_string() != raw {
                    continue;
                }
                let source = if row.try_get::<bool, _>("initial")?
                    && row.try_get::<&str, _>("owner_user_id")?
                        == row.try_get::<&str, _>("author_id")?
                    && let Some(asset_key) = row.try_get::<Option<String>, _>("asset_key")?
                {
                    let owner: String = row.try_get("owner_user_id")?;
                    let media_type: String = row.try_get("media_type")?;
                    Some(Source {
                        owner: owners
                            .entry(owner.clone())
                            .or_insert_with(|| Arc::from(owner))
                            .clone(),
                        media_type: media
                            .entry(media_type.clone())
                            .or_insert_with(|| Arc::from(media_type))
                            .clone(),
                        asset_key: asset_key.into_boxed_str(),
                        created_at: row.try_get("created_at")?,
                    })
                } else {
                    None
                };
                match images.entry(id) {
                    std::collections::hash_map::Entry::Vacant(entry) => {
                        entry.insert(source);
                    }
                    std::collections::hash_map::Entry::Occupied(mut entry) => {
                        entry.insert(None);
                    }
                }
            }
        }
        tx.commit().await?;
        let usable = images.values().filter(|v| v.is_some()).count();
        eprintln!(
            "{}",
            serde_json::json!({"metric":"initial_post_images_loaded","images":usable,"elapsed_ms":started.elapsed().as_millis()})
        );
        Ok(Self {
            reads: None,
            epoch,
            images,
            hits: AtomicU64::new(0),
            fallbacks: AtomicU64::new(0),
        })
    }

    pub async fn resolve<'a, 'c>(
        &'a self,
        db: impl Executor<'c, Database = Postgres>,
        id: &str,
    ) -> Result<Option<ResolvedImage<'a>>, sqlx::Error> {
        let source = Uuid::parse_str(id)
            .ok()
            .filter(|uuid| uuid.to_string() == id)
            .and_then(|id| self.images.get(&id))
            .and_then(Option::as_ref);
        let Some(source) = source else {
            self.fallbacks.fetch_add(1, Ordering::Relaxed);
            return Ok(None);
        };
        let key: Option<String> = sqlx::query_scalar("SELECT secret_key_hash FROM users WHERE id=$1 AND status='active' AND EXISTS(SELECT 1 FROM astra_initial_history_epoch WHERE singleton AND epoch=$2) AND NOT EXISTS(SELECT 1 FROM astra_initial_export_dirty WHERE owner_id=$1)")
            .bind(source.owner.as_ref()).bind(&self.epoch).fetch_optional(db).await?;
        let Some(secret_key_hash) = key else {
            self.fallbacks.fetch_add(1, Ordering::Relaxed);
            return Ok(None);
        };
        self.hits.fetch_add(1, Ordering::Relaxed);
        Ok(Some(ResolvedImage {
            owner: &source.owner,
            media_type: &source.media_type,
            asset_key: &source.asset_key,
            created_at: source.created_at,
            secret_key_hash,
        }))
    }

    pub fn counts(&self) -> (u64, u64) {
        (
            self.hits.load(Ordering::Relaxed),
            self.fallbacks.load(Ordering::Relaxed),
        )
    }

    pub fn observe(self: &Arc<Self>) {
        let weak = Arc::downgrade(self);
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(std::time::Duration::from_secs(10)).await;
                let Some(cache) = weak.upgrade() else {
                    break;
                };
                let (hits, fallbacks) = cache.counts();
                eprintln!(
                    "{}",
                    serde_json::json!({"metric":"initial_post_images","hits":hits,"fallbacks":fallbacks})
                );
            }
        });
    }
}
