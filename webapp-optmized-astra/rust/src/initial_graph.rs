//! Compute M and capped C in the application from an immutable initial graph.
//! Fresh changed edges, permissions, candidates and engagement share one
//! repeatable-read transaction. No asynchronous invalidation or TTL is used.
use crate::{
    api_error::Error,
    feed_api::GetFeedRequest,
    post::{POST_COLUMNS, PostWithAuthor},
    user::user_columns_as,
};
use sqlx::{FromRow, PgPool, Row};
use std::{
    collections::{HashMap, HashSet},
    sync::Arc,
};

type Graph = HashMap<Arc<str>, Arc<HashSet<Arc<str>>>>;
pub struct InitialGraph {
    epoch: String,
    graph: Graph,
}
fn change(graph: &mut Graph, low: &str, high: &str, active: bool) {
    let low = graph
        .get_key_value(low)
        .map(|(id, _)| id.clone())
        .unwrap_or_else(|| Arc::from(low));
    let high = graph
        .get_key_value(high)
        .map(|(id, _)| id.clone())
        .unwrap_or_else(|| Arc::from(high));
    for (from, to) in [(low.clone(), high.clone()), (high, low)] {
        let neighbors = Arc::make_mut(graph.entry(from).or_default());
        if active {
            neighbors.insert(to);
        } else {
            neighbors.remove(&to);
        }
    }
}
impl InitialGraph {
    pub async fn load(db: &PgPool) -> Result<Self, Error> {
        let mut tx = db.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
            .execute(&mut *tx)
            .await?;
        let epoch = sqlx::query_scalar("SELECT epoch FROM astra_graph_epoch WHERE singleton")
            .fetch_one(&mut *tx)
            .await?;
        let edges: Vec<(String, String)> =
            sqlx::query_as("SELECT user_low_id,user_high_id FROM initial_friendships")
                .fetch_all(&mut *tx)
                .await?;
        let mut graph = Graph::new();
        for (low, high) in &edges {
            change(&mut graph, low, high, true);
        }
        tx.commit().await?;
        eprintln!(
            "{}",
            serde_json::json!({"metric":"initial_graph_loaded","users":graph.len(),"edges":edges.len()})
        );
        Ok(Self { epoch, graph })
    }

    pub async fn recommended(
        &self,
        db: &PgPool,
        viewer: &str,
        input: &GetFeedRequest,
    ) -> Result<Option<Vec<(PostWithAuthor, i64)>>, Error> {
        let mut tx = db.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
            .execute(&mut *tx)
            .await?;
        let rows=sqlx::query("SELECT e.epoch,c.user_low_id,c.user_high_id,f.user_low_id IS NOT NULL AS active FROM astra_graph_epoch e LEFT JOIN astra_graph_changes c ON true LEFT JOIN friendships f ON f.user_low_id=c.user_low_id AND f.user_high_id=c.user_high_id WHERE e.singleton").fetch_all(&mut *tx).await?;
        if rows.is_empty() || rows[0].try_get::<&str, _>("epoch")? != self.epoch {
            tx.rollback().await?;
            return Ok(None);
        }
        let mut graph = self.graph.clone();
        for row in rows {
            if let Some(low) = row.try_get::<Option<&str>, _>("user_low_id")? {
                change(
                    &mut graph,
                    low,
                    row.try_get::<&str, _>("user_high_id")?,
                    row.try_get("active")?,
                );
            }
        }
        let friends = graph.get(viewer).cloned().unwrap_or_default();
        let mut mutual: HashMap<Arc<str>, i64> = HashMap::new();
        for friend in friends.iter() {
            if let Some(neighbors) = graph.get(friend) {
                for other in neighbors.iter().filter(|other| other.as_ref() != viewer) {
                    *mutual.entry(other.clone()).or_default() += 1;
                }
            }
        }
        let candidates: Vec<String> = mutual
            .keys()
            .filter(|id| !friends.contains(*id))
            .map(ToString::to_string)
            .collect();
        if candidates.is_empty() {
            tx.commit().await?;
            return Ok(Some(Vec::new()));
        }
        let membership = if candidates.len() <= 128 {
            "p.author_id=ANY($2::text[])"
        } else {
            "(p.author_id=ANY($2::text[])) IS TRUE"
        };
        let query = format!(
            r#"
/* astra_initial_graph_candidates */
WITH viewer AS (SELECT id,region FROM users WHERE id=$1 AND status='active'),
fof_posts AS MATERIALIZED (
 SELECT candidate.* FROM posting_windows w CROSS JOIN LATERAL (
  SELECT {POST_COLUMNS}, {authors} FROM posts p JOIN users u ON u.id=p.author_id AND u.status='active'
  WHERE p.posting_window_id=w.id AND p.deleted_at IS NULL AND {membership}
   AND p.visibility IN ('public','friends_of_friends')
   AND EXISTS(SELECT 1 FROM viewer v WHERE EXISTS(
    SELECT 1 FROM posts own JOIN posting_windows posted ON posted.id=own.posting_window_id
    JOIN posting_windows current ON current.region=v.region AND current.ended_at IS NULL AND current.local_date=posted.local_date
    WHERE own.author_id=v.id AND own.deleted_at IS NULL))
   AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocker_id=$1 AND b.blocked_id=p.author_id)
   AND NOT EXISTS(SELECT 1 FROM block_relations b WHERE b.blocked_id=$1 AND b.blocker_id=p.author_id)
 ) candidate WHERE w.ended_at IS NULL
), engaged AS (
 SELECT reaction.post_id,count(*)::bigint AS n FROM (
  SELECT r.post_id,r.reactor_id AS user_id FROM realmojis r WHERE r.post_id IN (SELECT id FROM fof_posts)
   AND r.replaced_at IS NULL AND r.deleted_at IS NULL AND r.reactor_id=ANY($3::text[])
  UNION
  SELECT c.post_id,c.author_id AS user_id FROM comments c WHERE c.post_id IN (SELECT id FROM fof_posts)
   AND c.deleted_at IS NULL AND c.author_id=ANY($3::text[])
 ) reaction JOIN users u ON u.id=reaction.user_id AND u.status='active' GROUP BY reaction.post_id
)
SELECT fof_posts.*,coalesce(engaged.n,0)::bigint AS engaged FROM fof_posts LEFT JOIN engaged ON engaged.post_id=fof_posts.id
"#,
            authors = user_columns_as("u", "author")
        );
        let friends: Vec<String> = friends.iter().map(ToString::to_string).collect();
        let rows = sqlx::query(&query)
            .bind(viewer)
            .bind(candidates)
            .bind(friends)
            .fetch_all(&mut *tx)
            .await?;
        tx.commit().await?;
        let mut posts = Vec::with_capacity(rows.len());
        let mut connections = HashMap::new();
        for row in rows {
            let post = PostWithAuthor::from_row(&row)?;
            let author = post.post.author_id.as_str();
            let c = *connections
                .entry(post.post.author_id.clone())
                .or_insert_with(|| {
                    let mut c = 0;
                    if let Some(neighbors) = graph.get(author) {
                        for other in neighbors.iter() {
                            c += mutual.get(other).copied().unwrap_or(0);
                            if c >= 30 {
                                return 30;
                            }
                        }
                    }
                    c
                });
            let score = 10 * row.try_get::<i64, _>("engaged")?
                + 3 * mutual.get(author).copied().unwrap_or(0)
                + c;
            if input.before_score.is_some_and(|before_score| {
                (score, post.post.posted_at, post.post.id.as_str())
                    >= (
                        before_score,
                        input.before_posted_at.unwrap(),
                        input.before_id.as_str(),
                    )
            }) {
                continue;
            }
            posts.push((post, score));
        }
        posts.sort_by(|(a, sa), (b, sb)| {
            (sb, b.post.posted_at, &b.post.id).cmp(&(sa, a.post.posted_at, &a.post.id))
        });
        posts.truncate(51);
        Ok(Some(posts))
    }
}
