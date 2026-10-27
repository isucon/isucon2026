use std::collections::HashSet;
use std::str::FromStr;
use std::sync::Arc;

use axum::{Extension, extract::State, http::StatusCode, response::IntoResponse};
use chrono::{DateTime, Utc};
use http_body_util::BodyExt;
use isureal::{
    HandlerContext,
    bind::Bind,
    config_from_env,
    feed_api::{GetFeedRequest, get_feed_handler},
    post::{PostWithAuthor, load_viewer_context, select_posts_with_author},
    session::Session,
};
use serde_json::Value;
use sqlx::{
    Arguments, PgPool,
    postgres::{PgArguments, PgConnectOptions, PgPoolOptions},
};

// The original Rust algorithm remains the independent reference for permissions
// and pagination. It loads all active posts and applies the graph in memory.
async fn reference(
    db: &PgPool,
    feed: &str,
    before: Option<DateTime<Utc>>,
    before_id: &str,
) -> Vec<PostWithAuthor> {
    let viewer = match load_viewer_context(&mut db.acquire().await.unwrap(), "viewer").await {
        Ok(viewer) => viewer,
        Err(err) if err.is_no_rows() => return Vec::new(),
        Err(err) => panic!("reference viewer: {err}"),
    };
    let following: HashSet<String> =
        sqlx::query_scalar("SELECT followee_id FROM follows WHERE follower_id = 'viewer'")
            .fetch_all(db)
            .await
            .unwrap()
            .into_iter()
            .collect();
    let mut args = PgArguments::default();
    args.add("viewer").unwrap();
    args.add(before).unwrap();
    args.add(before_id).unwrap();
    select_posts_with_author(
        db,
        r#"
      AND p.author_id <> $1 AND w.ended_at IS NULL
      AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
      ORDER BY p.posted_at DESC, p.id DESC
    "#,
        args,
    )
    .await
    .unwrap()
    .into_iter()
    .filter(|row| {
        viewer.can_view(&row.post)
            && match feed {
                "public" => row.post.visibility == "public",
                "friends" => viewer.friends.contains(&row.post.author_id),
                "friends-of-friends" => {
                    !viewer.friends.contains(&row.post.author_id)
                        && viewer.friends_of_friends.contains(&row.post.author_id)
                }
                "following" => following.contains(&row.post.author_id),
                _ => unreachable!(),
            }
    })
    .collect()
}

fn session() -> Session {
    Session {
        user_id: "viewer".into(),
        region: "asia".into(),
        token: String::new(),
        token_hash: String::new(),
        created_at: Utc::now(),
    }
}

async fn compare(h: &Arc<HandlerContext>, stage: &str) {
    for feed in ["public", "friends", "friends-of-friends", "following"] {
        let mut before = None;
        let mut before_id = String::new();
        loop {
            let expected = reference(&h.db, feed, before, &before_id).await;
            let input = GetFeedRequest {
                feed: feed.into(),
                before_posted_at: before,
                before_id: before_id.clone(),
            };
            let response = get_feed_handler(State(h.clone()), Extension(session()), Bind(input))
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            let bytes = response.into_body().collect().await.unwrap().to_bytes();
            let body: Value = serde_json::from_slice(&bytes).unwrap();
            let ids: Vec<&str> = body["items"]
                .as_array()
                .unwrap()
                .iter()
                .map(|item| item["id"].as_str().unwrap())
                .collect();
            let expected_ids: Vec<&str> = expected
                .iter()
                .take(50)
                .map(|row| row.post.id.as_str())
                .collect();
            assert_eq!(
                ids, expected_ids,
                "stage={stage}, feed={feed}, cursor={before_id}"
            );
            assert_eq!(
                body["hasMore"],
                expected.len() > 50,
                "stage={stage}, feed={feed}"
            );
            if expected.len() <= 50 {
                break;
            }
            before = Some(expected[49].post.posted_at);
            before_id = expected[49].post.id.clone();
        }
    }
}

#[tokio::test]
async fn feed_sql_matches_original_graph_and_filters_before_pagination() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let schema = format!("astra_feed_{}", uuid::Uuid::new_v4().simple());
    let admin = PgPool::connect(&dsn).await.unwrap();
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let options = PgConnectOptions::from_str(&dsn)
        .unwrap()
        .options([("search_path", schema.as_str())]);
    let db = PgPoolOptions::new()
        .max_connections(3)
        .connect_with(options)
        .await
        .unwrap();
    sqlx::raw_sql(r#"
      CREATE TABLE users (
        id text PRIMARY KEY, username text NOT NULL, secret_key_hash text NOT NULL,
        display_name text NOT NULL DEFAULT 'User', bio text NOT NULL DEFAULT '',
        profile_image_id text, region text NOT NULL DEFAULT 'asia',
        account_type text NOT NULL DEFAULT 'regular', status text NOT NULL DEFAULT 'active'
      );
      CREATE TABLE posting_windows (id text PRIMARY KEY, region text, local_date date, on_time_deadline timestamptz, ended_at timestamptz);
      CREATE TABLE posts (
        id text PRIMARY KEY, author_id text, posting_window_id text, image_id text,
        caption text NOT NULL DEFAULT '', visibility text, location_kind text NOT NULL DEFAULT 'none',
        latitude numeric, longitude numeric, locality text, posted_at timestamptz, deleted_at timestamptz
      );
      CREATE TABLE friendships (user_low_id text, user_high_id text);
      CREATE TABLE block_relations (blocker_id text, blocked_id text);
      CREATE TABLE follows (follower_id text, followee_id text);
      INSERT INTO users (id,username,secret_key_hash) VALUES ('viewer','viewer',repeat('ab',32));
      INSERT INTO users (id,username,secret_key_hash)
        SELECT 'author'||lpad(i::text,3,'0'), 'author'||i, repeat('cd',32) FROM generate_series(0,199) i;
      INSERT INTO posting_windows VALUES
        ('asia','asia','2026-10-01','2026-10-01 10:02Z',NULL),
        ('europe','europe','2026-10-02','2026-10-02 10:02Z',NULL),
        ('america','america','2026-10-01','2026-10-01 10:02Z',NULL),
        ('old','asia','2026-09-30','2026-09-30 10:02Z','2026-10-01 10:00Z');
      INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,posted_at)
        SELECT 'post'||lpad(i::text,3,'0'), 'author'||lpad(i::text,3,'0'),
          CASE WHEN i<60 THEN 'asia' WHEN i<65 THEN 'europe' WHEN i<70 THEN 'america'
            WHEN i%3=0 THEN 'asia' WHEN i%3=1 THEN 'europe' ELSE 'america' END, 'image'||i,
          CASE WHEN i<70 THEN 'public' WHEN i<90 THEN 'friends' WHEN i<110 THEN 'friends_of_friends' ELSE 'friends' END,
          '2026-10-01 10:00Z'::timestamptz + (i/3)*interval '1 second'
        FROM generate_series(0,199) i;
      INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,posted_at)
        VALUES ('old-post','author000','old','old-image','public','2026-09-30 10:00Z');
      INSERT INTO friendships SELECT 'viewer', 'author'||lpad(i::text,3,'0') FROM generate_series(0,4) i;
      INSERT INTO friendships SELECT CASE WHEN i%2=0 THEN 'viewer' ELSE 'author'||lpad(i::text,3,'0') END,
        CASE WHEN i%2=0 THEN 'author'||lpad(i::text,3,'0') ELSE 'viewer' END FROM generate_series(70,79) i;
      INSERT INTO friendships SELECT 'author070', 'author'||lpad(i::text,3,'0') FROM generate_series(90,99) i;
      INSERT INTO follows SELECT 'viewer', 'author'||lpad(i::text,3,'0') FROM generate_series(0,199) i WHERE i%2=0;
      INSERT INTO block_relations VALUES ('viewer','author001'),('author002','viewer'),('viewer','author072'),('author092','viewer');
      UPDATE users SET status='deleted' WHERE id='author003';
      UPDATE posts SET deleted_at=now() WHERE id='post004';
    "#).execute(&db).await.unwrap();
    let h = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
    });
    compare(&h, "not posted").await;
    sqlx::query("INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,posted_at) VALUES ('own','viewer','asia','own-image','public','2026-10-01 10:00Z')")
        .execute(&db).await.unwrap();
    assert_eq!(reference(&db, "public", None, "").await.len(), 66);
    compare(&h, "visible gate and mixed graph").await;
    sqlx::raw_sql("INSERT INTO block_relations VALUES ('author065','viewer'); INSERT INTO friendships VALUES ('viewer','author120'); DELETE FROM friendships WHERE user_low_id='viewer' AND user_high_id='author070';")
        .execute(&db).await.unwrap();
    compare(&h, "fresh block and friendship mutations").await;
    sqlx::query("UPDATE users SET status='deleted' WHERE id='viewer'")
        .execute(&db)
        .await
        .unwrap();
    compare(&h, "deleted viewer").await;
    sqlx::query("UPDATE users SET status='active',region='europe' WHERE id='viewer'")
        .execute(&db)
        .await
        .unwrap();
    compare(&h, "different regional date closes gate").await;
    sqlx::query("UPDATE posting_windows SET local_date='2026-10-01' WHERE id='europe'")
        .execute(&db)
        .await
        .unwrap();
    compare(&h, "same date after region change opens gate").await;
    sqlx::raw_sql("UPDATE posting_windows SET ended_at=now() WHERE id='asia'; INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,posted_at) VALUES ('foreign','author000','europe','foreign-image','public','2026-10-01 10:00Z');")
        .execute(&db).await.unwrap();
    compare(&h, "ended source window and active foreign post").await;
    sqlx::query("UPDATE posts SET deleted_at=now() WHERE id='own'")
        .execute(&db)
        .await
        .unwrap();
    compare(&h, "deleting own post closes gate").await;
    for input in [
        GetFeedRequest {
            feed: "invalid".into(),
            ..Default::default()
        },
        GetFeedRequest {
            feed: "public".into(),
            before_id: "cursor".into(),
            ..Default::default()
        },
        GetFeedRequest {
            feed: "public".into(),
            before_posted_at: Some(Utc::now()),
            ..Default::default()
        },
    ] {
        let error = get_feed_handler(State(h.clone()), Extension(session()), Bind(input))
            .await
            .unwrap_err();
        assert_eq!(error.into_response().status(), StatusCode::BAD_REQUEST);
    }
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
