//! Immutable recipient snapshots, cached only while their posting window is open.
use std::collections::HashMap;

use chrono::{DateTime, Utc};
use sqlx::PgPool;

pub(crate) struct Batch {
    pub created_at: DateTime<Utc>,
    pub notifications: HashMap<String, String>,
}

#[derive(Default)]
pub(crate) struct Batches(pub HashMap<String, Batch>);

#[derive(sqlx::FromRow)]
struct StoredBatch {
    posting_window_id: String,
    user_ids: Vec<String>,
    notification_ids: Vec<String>,
    created_at: DateTime<Utc>,
}

impl Batches {
    pub async fn refresh(&mut self, db: &PgPool) -> Result<(), sqlx::Error> {
        let active: Vec<String> =
            sqlx::query_scalar("SELECT id FROM posting_windows WHERE ended_at IS NULL")
                .fetch_all(db)
                .await?;
        self.0.retain(|id, _| active.contains(id));
        let missing: Vec<&str> = active
            .iter()
            .filter(|id| !self.0.contains_key(*id))
            .map(String::as_str)
            .collect();
        if missing.is_empty() {
            return Ok(());
        }
        let rows = sqlx::query_as::<_, StoredBatch>(
            "SELECT posting_window_id,user_ids,notification_ids,created_at \
             FROM astra_notification_batches WHERE posting_window_id=ANY($1)",
        )
        .bind(missing)
        .fetch_all(db)
        .await?;
        for row in rows {
            self.0.insert(
                row.posting_window_id,
                Batch {
                    created_at: row.created_at,
                    notifications: row.user_ids.into_iter().zip(row.notification_ids).collect(),
                },
            );
        }
        Ok(())
    }
}
