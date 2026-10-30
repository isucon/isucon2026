use std::collections::{HashMap, HashSet};
use std::str::FromStr;
use std::sync::Arc;

use axum::{Extension, extract::State, http::StatusCode, response::IntoResponse};
use chrono::{DateTime, Utc};
use http_body_util::BodyExt;
use isureal::{
    HandlerContext,
    bind::Bind,
    comment::can_comment,
    config_from_env,
    feed_api::{GetFeedRequest, get_feed_handler},
    post::{PostWithAuthor, can_view_post, load_viewer_context, select_posts_with_author},
    post_api::{GetPostRequest, get_post_handler},
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

// Independent in-memory reference for the recommended friends-of-friends order:
// 10 * E + 3 * M + min(C, 30) from raw friendships, RealMojis and comments.
async fn recommendation_scores(db: &PgPool, posts: &[PostWithAuthor]) -> HashMap<String, i64> {
    let friendships: Vec<(String, String)> =
        sqlx::query_as("SELECT user_low_id, user_high_id FROM friendships")
            .fetch_all(db)
            .await
            .unwrap();
    let friends: HashSet<&str> = friendships
        .iter()
        .filter_map(|(low, high)| match (low.as_str(), high.as_str()) {
            ("viewer", other) | (other, "viewer") => Some(other),
            _ => None,
        })
        .collect();
    let mut mutual: HashMap<&str, i64> = HashMap::new();
    for (low, high) in &friendships {
        if friends.contains(low.as_str()) && high != "viewer" {
            *mutual.entry(high).or_default() += 1;
        }
        if friends.contains(high.as_str()) && low != "viewer" {
            *mutual.entry(low).or_default() += 1;
        }
    }
    let active: HashSet<String> =
        sqlx::query_scalar("SELECT id FROM users WHERE status = 'active'")
            .fetch_all(db)
            .await
            .unwrap()
            .into_iter()
            .collect();
    let engagements: Vec<(String, String)> = sqlx::query_as(
        "SELECT post_id, reactor_id FROM realmojis WHERE replaced_at IS NULL AND deleted_at IS NULL \
         UNION ALL SELECT post_id, author_id FROM comments WHERE deleted_at IS NULL",
    )
    .fetch_all(db)
    .await
    .unwrap();
    posts
        .iter()
        .map(|post| {
            let author = post.post.author_id.as_str();
            let mutual_friends = mutual.get(author).copied().unwrap_or_default();
            let connections: i64 = friendships
                .iter()
                .map(|(low, high)| {
                    let other = if low == author {
                        high
                    } else if high == author {
                        low
                    } else {
                        return 0;
                    };
                    mutual.get(other.as_str()).copied().unwrap_or_default()
                })
                .sum();
            let engaged: HashSet<&str> = engagements
                .iter()
                .filter(|(post_id, user)| {
                    post_id == &post.post.id
                        && friends.contains(user.as_str())
                        && active.contains(user)
                })
                .map(|(_, user)| user.as_str())
                .collect();
            let score = 10 * engaged.len() as i64 + 3 * mutual_friends + connections.min(30);
            (post.post.id.clone(), score)
        })
        .collect()
}

async fn ranked_reference(db: &PgPool) -> Vec<(PostWithAuthor, i64)> {
    let candidates = reference(db, "friends-of-friends", None, "").await;
    let scores = recommendation_scores(db, &candidates).await;
    let mut ranked: Vec<(PostWithAuthor, i64)> = candidates
        .into_iter()
        .map(|post| {
            let score = scores[&post.post.id];
            (post, score)
        })
        .collect();
    // Stable: equal scores keep the reference's postedAt DESC, id DESC order.
    ranked.sort_by_key(|a| std::cmp::Reverse(a.1));
    ranked
}

async fn compare_ranked(h: &Arc<HandlerContext>, stage: &str) -> usize {
    let graph = Arc::new(
        isureal::initial_graph::InitialGraph::load(&h.db)
            .await
            .unwrap(),
    );
    let all = ranked_reference(&h.db).await;
    let remote = isureal::recommendation_reads::RecommendationReads {
        all_feeds: false,
        db: PgPoolOptions::new()
            .max_connections(1)
            .connect_with((*h.db.connect_options()).clone())
            .await
            .unwrap(),
    };
    let mut occupied = Vec::new();
    for _ in 0..3 {
        occupied.push(h.db.acquire().await.unwrap());
    }
    let mut cursor: Option<(i64, DateTime<Utc>, String)> = None;
    let mut pages = 0;
    loop {
        let expected: Vec<&(PostWithAuthor, i64)> = all
            .iter()
            .filter(|(post, score)| {
                cursor
                    .as_ref()
                    .is_none_or(|(before_score, before, before_id)| {
                        (*score, post.post.posted_at, post.post.id.as_str())
                            < (*before_score, *before, before_id.as_str())
                    })
            })
            .collect();
        for cached_graph in [None, Some(Extension(graph.clone()))] {
            let input = GetFeedRequest {
                feed: "friends-of-friends".into(),
                before_score: cursor.as_ref().map(|cursor| cursor.0),
                before_posted_at: cursor.as_ref().map(|cursor| cursor.1),
                before_id: cursor
                    .as_ref()
                    .map(|cursor| cursor.2.clone())
                    .unwrap_or_default(),
            };
            let response = get_feed_handler(
                State(h.clone()),
                Extension(session()),
                cached_graph,
                Some(Extension(remote.clone())),
                Bind(input),
            )
            .await
            .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            let bytes = response.into_body().collect().await.unwrap().to_bytes();
            let body: Value = serde_json::from_slice(&bytes).unwrap();
            let items: Vec<(&str, i64)> = body["items"]
                .as_array()
                .unwrap()
                .iter()
                .map(|item| {
                    (
                        item["id"].as_str().unwrap(),
                        item["score"].as_i64().unwrap(),
                    )
                })
                .collect();
            let expected_items: Vec<(&str, i64)> = expected
                .iter()
                .take(50)
                .map(|(post, score)| (post.post.id.as_str(), *score))
                .collect();
            assert_eq!(items, expected_items, "ranked stage={stage}, page={pages}");
            assert_eq!(
                body["hasMore"],
                expected.len() > 50,
                "ranked stage={stage}, page={pages}"
            );
        }
        pages += 1;
        if expected.len() <= 50 {
            return pages;
        }
        let last = expected[49];
        cursor = Some((last.1, last.0.post.posted_at, last.0.post.id.clone()));
    }
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

async fn compare(h: &Arc<HandlerContext>, stage: &str) -> usize {
    compare_post_permissions(h, stage).await;
    let remote = isureal::recommendation_reads::RecommendationReads {
        all_feeds: true,
        db: PgPoolOptions::new()
            .max_connections(1)
            .connect_with(
                (*h.db.connect_options())
                    .clone()
                    .options([("default_transaction_read_only", "on")]),
            )
            .await
            .unwrap(),
    };
    for feed in ["public", "friends", "following"] {
        let mut before = None;
        let mut before_id = String::new();
        loop {
            let expected = reference(&h.db, feed, before, &before_id).await;
            for remote in [None, Some(Extension(remote.clone()))] {
                let mut occupied = Vec::new();
                if remote.is_some() {
                    for _ in 0..3 {
                        occupied.push(h.db.acquire().await.unwrap());
                    }
                }
                // Other feeds ignore an integer beforeScore.
                let input = GetFeedRequest {
                    feed: feed.into(),
                    before_score: before.map(|_| 7),
                    before_posted_at: before,
                    before_id: before_id.clone(),
                };
                let response = get_feed_handler(
                    State(h.clone()),
                    Extension(session()),
                    None,
                    remote,
                    Bind(input),
                )
                .await
                .unwrap();
                assert_eq!(response.status(), StatusCode::OK);
                let bytes = response.into_body().collect().await.unwrap().to_bytes();
                let body: Value = serde_json::from_slice(&bytes).unwrap();
                let ids: Vec<&str> = body["items"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|item| {
                        assert!(item.get("score").is_none(), "feed={feed} has no score");
                        item["id"].as_str().unwrap()
                    })
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
                drop(occupied);
            }
            if expected.len() <= 50 {
                break;
            }
            before = Some(expected[49].post.posted_at);
            before_id = expected[49].post.id.clone();
        }
    }
    compare_ranked(h, stage).await
}

async fn compare_post_permissions(h: &Arc<HandlerContext>, stage: &str) {
    let viewer = match load_viewer_context(&mut h.db.acquire().await.unwrap(), "viewer").await {
        Ok(viewer) => Some(viewer),
        Err(err) if err.is_no_rows() => None,
        Err(err) => panic!("reference viewer: {err}"),
    };
    let posts = select_posts_with_author(&h.db, "", PgArguments::default())
        .await
        .unwrap();
    let mut ids: Vec<String> = sqlx::query_scalar("SELECT id FROM posts ORDER BY id")
        .fetch_all(&h.db)
        .await
        .unwrap();
    ids.push("nonexistent".into());
    for id in ids {
        let post = posts.iter().find(|post| post.post.id == id);
        let expected_view = post
            .zip(viewer.as_ref())
            .is_some_and(|(p, v)| v.can_view(&p.post));
        let expected_comment = post.zip(viewer.as_ref()).is_some_and(|(p, v)| {
            v.can_view(&p.post)
                && p.post.window_ended_at.is_none()
                && if v.user.id == p.post.author_id {
                    v.has_posted_today
                } else {
                    p.post.visibility == "public" || v.friends.contains(&p.post.author_id)
                }
        });
        assert_eq!(
            can_view_post(&mut h.db.acquire().await.unwrap(), "viewer", &id)
                .await
                .unwrap(),
            expected_view,
            "view stage={stage}, post={id}"
        );
        assert_eq!(
            can_comment(&mut h.db.acquire().await.unwrap(), "viewer", &id)
                .await
                .unwrap(),
            expected_comment,
            "comment stage={stage}, post={id}"
        );
        let response = get_post_handler(
            State(h.clone()),
            Extension(session()),
            Bind(GetPostRequest {
                post_id: id.clone(),
            }),
        )
        .await
        .into_response();
        assert_eq!(
            response.status(),
            if expected_view {
                StatusCode::OK
            } else {
                StatusCode::NOT_FOUND
            },
            "detail stage={stage}, post={id}"
        );
        if expected_view {
            let body: Value =
                serde_json::from_slice(&response.into_body().collect().await.unwrap().to_bytes())
                    .unwrap();
            let expected = post.unwrap();
            assert_eq!(body["id"], expected.post.id);
            assert_eq!(body["caption"], expected.post.caption);
            assert_eq!(body["visibility"], expected.post.visibility);
            assert_eq!(body["author"]["id"], expected.author.id);
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
    let options = PgConnectOptions::from_str(&dsn).unwrap().options([
        ("search_path", schema.as_str()),
        ("plan_cache_mode", "force_generic_plan"),
    ]);
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
      CREATE TABLE realmojis (post_id text, reactor_id text, replaced_at timestamptz, deleted_at timestamptz);
      CREATE TABLE comments (post_id text, author_id text, deleted_at timestamptz);
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
    sqlx::raw_sql("CREATE TABLE initial_friendships AS SELECT * FROM friendships")
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/022-initial-graph.sql"))
        .execute(&db)
        .await
        .unwrap();
    let h = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: false,
        read_fence: None,
        image_store: None,
    });
    compare(&h, "not posted").await;
    sqlx::query("INSERT INTO posts (id,author_id,posting_window_id,image_id,visibility,posted_at) VALUES ('own','viewer','asia','own-image','public','2026-10-01 10:00Z')")
        .execute(&db).await.unwrap();
    assert_eq!(reference(&db, "public", None, "").await.len(), 66);
    compare(&h, "visible gate and mixed graph").await;
    // Recommendation inputs: mutual friends (070 and 071 know 090/091), a
    // two-step path via 150, live and dead reactions, duplicate engagement by
    // one friend, a deleted friend (003) and a non-friend reactor (150).
    sqlx::raw_sql(r#"
      INSERT INTO friendships VALUES ('author071','author090'),('author071','author091'),
        ('author090','author150'),('author072','author150'),('author091','author150');
      INSERT INTO realmojis VALUES ('post090','author070',NULL,NULL),('post090','author071',NULL,NULL),
        ('post090','author072',now(),NULL),('post091','author150',NULL,NULL),('post093','author003',NULL,NULL),
        ('post094','author076',NULL,now());
      INSERT INTO comments VALUES ('post090','author071',NULL),('post090','author073',now()),
        ('post091','author074',NULL),('post091','author074',NULL),('post094','author078',NULL);
    "#).execute(&db).await.unwrap();
    let scores =
        recommendation_scores(&db, &reference(&db, "friends-of-friends", None, "").await).await;
    // post090: E=2 (070 RealMoji, 071 RealMoji and comment), M=2 (070, 071), C=1 (150 via 072).
    assert_eq!(scores["post090"], 10 * 2 + 3 * 2 + 1);
    // post091: E=1 (074 commented twice), M=2, C=1.
    assert_eq!(scores["post091"], 10 + 3 * 2 + 1);
    // post093: the deleted friend's RealMoji does not count.
    assert_eq!(scores["post093"], 3);
    assert_eq!(scores["post094"], 10 + 3);
    compare(&h, "recommendation inputs").await;
    sqlx::raw_sql("INSERT INTO friendships SELECT 'author000', 'author'||lpad(i::text,3,'0') FROM generate_series(5,199) i; UPDATE posts SET visibility='friends_of_friends' WHERE id>='post110' AND id<='post199';")
        .execute(&db).await.unwrap();
    assert!(reference(&db, "friends-of-friends", None, "").await.len() > 50);
    // author095 has 41 more friends that each share author000 with the viewer.
    sqlx::raw_sql("INSERT INTO friendships SELECT 'author095', 'author'||lpad(i::text,3,'0') FROM generate_series(100,140) i;")
        .execute(&db).await.unwrap();
    let scores =
        recommendation_scores(&db, &reference(&db, "friends-of-friends", None, "").await).await;
    // M=2 (000 and 070); C is capped at 30.
    assert_eq!(scores["post095"], 3 * 2 + 30);
    assert!(compare(&h, "dense graph and multiple pages").await > 1);
    sqlx::raw_sql("DELETE FROM friendships WHERE user_low_id='author000'; UPDATE posts SET visibility='friends' WHERE id>='post110' AND id<='post199';")
        .execute(&db).await.unwrap();
    compare(&h, "sparse graph restored").await;

    sqlx::raw_sql("INSERT INTO block_relations VALUES ('author065','viewer'); INSERT INTO friendships VALUES ('viewer','author120'); DELETE FROM friendships WHERE user_low_id='viewer' AND user_high_id='author070';")
        .execute(&db).await.unwrap();
    compare(&h, "fresh block and friendship mutations").await;
    // Keep one loaded graph across rollback, concurrent writes, and reset.
    let cached_graph = Arc::new(
        isureal::initial_graph::InitialGraph::load(&db)
            .await
            .unwrap(),
    );
    let graph_input = GetFeedRequest {
        feed: "friends-of-friends".into(),
        ..Default::default()
    };
    let expected_graph: Vec<_> = ranked_reference(&db)
        .await
        .into_iter()
        .take(51)
        .map(|(post, score)| (post.post.id, score))
        .collect();
    assert!(!expected_graph.is_empty());
    let mut rolled_back = db.begin().await.unwrap();
    sqlx::query("DELETE FROM friendships")
        .execute(&mut *rolled_back)
        .await
        .unwrap();
    rolled_back.rollback().await.unwrap();
    let actual: Vec<_> = cached_graph
        .recommended(&db, "viewer", &graph_input)
        .await
        .unwrap()
        .unwrap()
        .into_iter()
        .map(|(post, score)| (post.post.id, score))
        .collect();
    assert_eq!(
        actual, expected_graph,
        "rolled back changes remain invisible"
    );

    // Block candidate SQL after the graph snapshot, then commit a graph change.
    // One response must not mix old edges with new permissions or engagement.
    let mut writer = db.begin().await.unwrap();
    sqlx::query("LOCK TABLE users IN ACCESS EXCLUSIVE MODE")
        .execute(&mut *writer)
        .await
        .unwrap();
    let reader_db = db.clone();
    let reader_graph = cached_graph.clone();
    let reader = tokio::spawn(async move {
        reader_graph
            .recommended(
                &reader_db,
                "viewer",
                &GetFeedRequest {
                    feed: "friends-of-friends".into(),
                    ..Default::default()
                },
            )
            .await
            .unwrap()
            .unwrap()
    });
    let mut blocked = false;
    for _ in 0..200 {
        blocked = sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%astra_initial_graph_candidates%')")
            .fetch_one(&admin).await.unwrap();
        if blocked {
            break;
        }
        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
    }
    assert!(
        blocked,
        "candidate query reached the lock after reading graph changes"
    );
    sqlx::query("DELETE FROM friendships")
        .execute(&mut *writer)
        .await
        .unwrap();
    sqlx::query("UPDATE users SET display_name='Concurrent name' WHERE id='author090'")
        .execute(&mut *writer)
        .await
        .unwrap();
    writer.commit().await.unwrap();
    let actual: Vec<_> = reader
        .await
        .unwrap()
        .into_iter()
        .map(|(post, score)| (post.post.id, score))
        .collect();
    assert_eq!(
        actual, expected_graph,
        "single snapshot during concurrent mutation"
    );
    assert!(
        cached_graph
            .recommended(&db, "viewer", &graph_input)
            .await
            .unwrap()
            .unwrap()
            .is_empty()
    );

    let mut reset = db.begin().await.unwrap();
    sqlx::raw_sql("SET LOCAL session_replication_role=replica; TRUNCATE friendships,astra_graph_changes; INSERT INTO friendships SELECT * FROM initial_friendships;")
        .execute(&mut *reset).await.unwrap();
    reset.commit().await.unwrap();
    let expected_reset: Vec<_> = ranked_reference(&db)
        .await
        .into_iter()
        .take(51)
        .map(|(post, score)| (post.post.id, score))
        .collect();
    let actual: Vec<_> = cached_graph
        .recommended(&db, "viewer", &graph_input)
        .await
        .unwrap()
        .unwrap()
        .into_iter()
        .map(|(post, score)| (post.post.id, score))
        .collect();
    assert_eq!(
        actual, expected_reset,
        "reset restores the immutable graph with an empty overlay"
    );
    sqlx::query("DELETE FROM initial_friendships WHERE user_low_id='author070'")
        .execute(&db)
        .await
        .unwrap();
    assert!(
        cached_graph
            .recommended(&db, "viewer", &graph_input)
            .await
            .unwrap()
            .is_none(),
        "epoch mismatch falls back to SQL"
    );
    compare_ranked(&h, "reload after initial graph change").await;
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
        GetFeedRequest {
            feed: "friends-of-friends".into(),
            before_posted_at: Some(Utc::now()),
            before_id: "cursor".into(),
            ..Default::default()
        },
        GetFeedRequest {
            feed: "friends-of-friends".into(),
            before_score: Some(3),
            ..Default::default()
        },
    ] {
        let error = get_feed_handler(
            State(h.clone()),
            Extension(session()),
            None,
            None,
            Bind(input),
        )
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
