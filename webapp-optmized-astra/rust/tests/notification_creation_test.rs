use std::str::FromStr;
use std::sync::Arc;
use std::time::Duration;

use axum::{extract::State, http::StatusCode, response::IntoResponse};
use chrono::{DateTime, Utc};
use http_body_util::BodyExt;
use isureal::{
    HandlerContext,
    admin_posting_window_api::{OpenPostingWindowRequest, open_posting_window_handler},
    bind::Bind,
    config_from_env,
};
use serde_json::Value;
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use tokio::time::{sleep, timeout};
use uuid::Uuid;

async fn open(h: &Arc<HandlerContext>, region: &str, date: &str) -> (StatusCode, Value) {
    let response = open_posting_window_handler(
        State(h.clone()),
        Bind(OpenPostingWindowRequest {
            region: region.into(),
            local_date: date.into(),
            on_time_seconds: Some(120),
        }),
    )
    .await
    .into_response();
    let status = response.status();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    (status, serde_json::from_slice(&body).unwrap())
}

#[tokio::test]
async fn ordered_notification_batches_preserve_recipients_conflicts_and_atomicity() {
    verify_creation(false).await;
    verify_creation(true).await;
}

async fn verify_creation(batch_writes: bool) {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let schema = format!("astra_window_{}", Uuid::new_v4().simple());
    let admin = PgPool::connect(&dsn).await.unwrap();
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(8)
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
      CREATE TABLE users(id varchar(36) PRIMARY KEY, region text NOT NULL, status text NOT NULL);
      CREATE TABLE posting_windows(
        id varchar(36) PRIMARY KEY, region text NOT NULL, local_date date NOT NULL,
        starts_at timestamptz NOT NULL, on_time_deadline timestamptz NOT NULL, ended_at timestamptz
      );
      CREATE TABLE notifications(
        id varchar(36) PRIMARY KEY, user_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
        posting_window_id varchar(36) NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,
        created_at timestamptz NOT NULL, UNIQUE(user_id,posting_window_id)
      );
      INSERT INTO users SELECT 'user'||lpad(i::text,5,'0'),'asia','active' FROM generate_series(1,5000) i;
      INSERT INTO users VALUES('inactive','asia','deleted'),('foreign','europe','active');
    "#).execute(&db).await.unwrap();
    sqlx::raw_sql(include_str!("../../sql/008-notification-window-index.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/016-notification-region-locks.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::query("ALTER TABLE users ADD COLUMN bio text")
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/017-notification-batches.sql"))
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE VIEW observed_notifications AS SELECT * FROM notifications UNION ALL SELECT n.id,n.user_id,b.posting_window_id,b.created_at FROM astra_notification_batches b CROSS JOIN LATERAL unnest(b.notification_ids,b.user_ids) AS n(id,user_id)")
        .execute(&db).await.unwrap();
    let h = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: batch_writes,
        read_fence: None,
        image_store: None,
    });
    let (status, first) = open(&h, "asia", "2026-10-01").await;
    assert_eq!(status, StatusCode::CREATED, "{first}");
    let first_id = first["id"].as_str().unwrap();
    let starts: DateTime<Utc> = first["startsAt"].as_str().unwrap().parse().unwrap();
    let deadline: DateTime<Utc> = first["onTimeDeadline"].as_str().unwrap().parse().unwrap();
    assert_eq!((deadline - starts).num_seconds(), 120);
    let notices: Vec<(String, String, DateTime<Utc>)> = sqlx::query_as(
        "SELECT id,user_id,created_at FROM observed_notifications WHERE posting_window_id=$1 ORDER BY user_id"
    ).bind(first_id).fetch_all(&db).await.unwrap();
    assert_eq!(notices.len(), 5000);
    for (i, (id, user, created)) in notices.iter().enumerate() {
        let uuid = Uuid::parse_str(id).unwrap();
        assert_eq!(uuid.get_version_num(), 7);
        assert_eq!(uuid.get_variant(), uuid::Variant::RFC4122);
        assert_eq!(*user, format!("user{:05}", i + 1));
        assert_eq!(*created, starts);
        if i > 0 {
            assert!(notices[i - 1].0 < *id);
        }
    }
    assert_eq!(open(&h, "asia", "2026-10-01").await.0, StatusCode::CONFLICT);
    assert_eq!(open(&h, "asia", "2026-09-30").await.0, StatusCode::CONFLICT);
    // A later window must re-evaluate all recipients, including region moves.
    sqlx::raw_sql("UPDATE users SET region='europe' WHERE id='user00001'; UPDATE users SET status='deleted' WHERE id='user00002'; UPDATE users SET region='asia' WHERE id='foreign';")
        .execute(&db).await.unwrap();
    let (a, b) = tokio::join!(
        open(&h, "asia", "2026-10-02"),
        open(&h, "asia", "2026-10-02")
    );
    assert!(
        (a.0 == StatusCode::CREATED && b.0 == StatusCode::CONFLICT)
            || (b.0 == StatusCode::CREATED && a.0 == StatusCode::CONFLICT)
    );
    let second = if a.0 == StatusCode::CREATED { a.1 } else { b.1 };
    let second_id = second["id"].as_str().unwrap();
    let actual: Vec<String> = sqlx::query_scalar(
        "SELECT user_id FROM observed_notifications WHERE posting_window_id=$1 ORDER BY user_id",
    )
    .bind(second_id)
    .fetch_all(&db)
    .await
    .unwrap();
    let eligible: Vec<String> = sqlx::query_scalar(
        "SELECT id FROM users WHERE region='asia' AND status='active' ORDER BY id",
    )
    .fetch_all(&db)
    .await
    .unwrap();
    assert_eq!(actual, eligible);
    let ended: DateTime<Utc> =
        sqlx::query_scalar("SELECT ended_at FROM posting_windows WHERE id=$1")
            .bind(first_id)
            .fetch_one(&db)
            .await
            .unwrap();
    let second_start: DateTime<Utc> = second["startsAt"].as_str().unwrap().parse().unwrap();
    assert_eq!(ended, second_start);
    // Failure partway through the bulk insert must roll back the new window,
    // all new notices, and the previous window's ended_at update together.
    let (target, condition) = if batch_writes {
        (
            "astra_notification_batches",
            "NEW.user_ids @> ARRAY['user00010']::text[]",
        )
    } else {
        ("notifications", "NEW.user_id='user00010'")
    };
    sqlx::raw_sql(&format!(r#"
      CREATE FUNCTION reject_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
        IF {condition} THEN RAISE EXCEPTION 'injected notification failure'; END IF;
        RETURN NEW;
      END $$;
      CREATE TRIGGER reject_notice BEFORE INSERT ON {target} FOR EACH ROW EXECUTE FUNCTION reject_notice();
    "#)).execute(&db).await.unwrap();
    let before: i64 = sqlx::query_scalar("SELECT count(*) FROM observed_notifications")
        .fetch_one(&db)
        .await
        .unwrap();
    assert_eq!(
        open(&h, "asia", "2026-10-03").await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM observed_notifications")
            .fetch_one(&db)
            .await
            .unwrap(),
        before
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM posting_windows")
            .fetch_one(&db)
            .await
            .unwrap(),
        2
    );
    assert!(
        sqlx::query_scalar::<_, bool>("SELECT ended_at IS NULL FROM posting_windows WHERE id=$1")
            .bind(second_id)
            .fetch_one(&db)
            .await
            .unwrap()
    );
    sqlx::query(&format!("DROP TRIGGER reject_notice ON {target}"))
        .execute(&db)
        .await
        .unwrap();
    let (status, empty) = open(&h, "america", "2026-10-01").await;
    assert_eq!(status, StatusCode::CREATED);
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM observed_notifications WHERE posting_window_id=$1"
        )
        .bind(empty["id"].as_str().unwrap())
        .fetch_one(&db)
        .await
        .unwrap(),
        0
    );
    sqlx::query("UPDATE users SET region='europe' WHERE id='foreign'")
        .execute(&db)
        .await
        .unwrap();
    // Hold Asia inside its insert after it freezes user eligibility. Europe
    // must still finish, while a new user must wait until Asia has committed.
    sqlx::raw_sql(r#"
      CREATE FUNCTION hold_asia_window() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
        IF NEW.region='asia' AND NEW.local_date='2026-10-04'::date THEN
          PERFORM pg_advisory_xact_lock(700001);
        END IF;
        RETURN NEW;
      END $$;
      CREATE TRIGGER hold_asia BEFORE INSERT ON posting_windows FOR EACH ROW EXECUTE FUNCTION hold_asia_window();
    "#).execute(&db).await.unwrap();
    let mut held = db.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(700001)")
        .execute(&mut *held)
        .await
        .unwrap();
    let asia = h.clone();
    let mut asia = tokio::spawn(async move { open(&asia, "asia", "2026-10-04").await });
    timeout(Duration::from_secs(2), async {
        loop {
            let waiting: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=700001 AND NOT granted)")
                .fetch_one(&db).await.unwrap();
            if waiting { break; }
            sleep(Duration::from_millis(5)).await;
        }
    }).await.unwrap();
    assert_eq!(
        timeout(Duration::from_secs(2), open(&h, "europe", "2026-10-04"))
            .await
            .unwrap()
            .0,
        StatusCode::CREATED
    );
    assert!(timeout(Duration::from_millis(50), &mut asia).await.is_err());
    let insert_db = db.clone();
    let mut registered = tokio::spawn(async move {
        sqlx::query("INSERT INTO users(id,region,status) VALUES ('after_asia','asia','active')")
            .execute(&insert_db)
            .await
            .unwrap();
    });
    assert!(
        timeout(Duration::from_millis(100), &mut registered)
            .await
            .is_err()
    );
    // Updates unrelated to eligibility and other-region registrations must
    // complete even while Asia is held inside its window transaction.
    timeout(
        Duration::from_secs(1),
        sqlx::query("UPDATE users SET bio='edited' WHERE id='user00003'").execute(&db),
    )
    .await
    .unwrap()
    .unwrap();
    timeout(
        Duration::from_secs(1),
        sqlx::query(
            "INSERT INTO users(id,region,status) VALUES ('during_europe','europe','active')",
        )
        .execute(&db),
    )
    .await
    .unwrap()
    .unwrap();
    let move_db = db.clone();
    let mut moved = tokio::spawn(async move {
        sqlx::query("UPDATE users SET region='asia' WHERE id='foreign'")
            .execute(&move_db)
            .await
            .unwrap();
    });
    let status_db = db.clone();
    let mut activated = tokio::spawn(async move {
        sqlx::query("UPDATE users SET status='active' WHERE id='inactive'")
            .execute(&status_db)
            .await
            .unwrap();
    });
    assert!(
        timeout(Duration::from_millis(50), &mut moved)
            .await
            .is_err()
    );
    assert!(
        timeout(Duration::from_millis(50), &mut activated)
            .await
            .is_err()
    );
    held.commit().await.unwrap();
    let (status, asia_window) = timeout(Duration::from_secs(3), asia)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(status, StatusCode::CREATED);
    timeout(Duration::from_secs(2), registered)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM observed_notifications WHERE user_id='after_asia' AND posting_window_id=$1"
        )
        .bind(asia_window["id"].as_str().unwrap())
        .fetch_one(&db)
        .await
        .unwrap(),
        0
    );
    timeout(Duration::from_secs(2), moved)
        .await
        .unwrap()
        .unwrap();
    timeout(Duration::from_secs(2), activated)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(sqlx::query_scalar::<_, i64>("SELECT count(*) FROM observed_notifications WHERE posting_window_id=$1 AND user_id IN ('foreign','inactive','during_europe')")
        .bind(asia_window["id"].as_str().unwrap()).fetch_one(&db).await.unwrap(), 0);
    let (status, next_window) = open(&h, "asia", "2026-10-05").await;
    assert_eq!(status, StatusCode::CREATED);
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM observed_notifications WHERE user_id='after_asia' AND posting_window_id=$1"
        )
        .bind(next_window["id"].as_str().unwrap())
        .fetch_one(&db)
        .await
        .unwrap(),
        1
    );
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
