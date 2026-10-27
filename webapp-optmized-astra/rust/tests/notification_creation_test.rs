use std::str::FromStr;
use std::sync::Arc;

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
        .max_connections(4)
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
    let h = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
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
        "SELECT id,user_id,created_at FROM notifications WHERE posting_window_id=$1 ORDER BY user_id"
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
        "SELECT user_id FROM notifications WHERE posting_window_id=$1 ORDER BY user_id",
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
    sqlx::raw_sql(r#"
      CREATE FUNCTION reject_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
        IF NEW.user_id='user00010' THEN RAISE EXCEPTION 'injected notification failure'; END IF;
        RETURN NEW;
      END $$;
      CREATE TRIGGER reject_notice BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION reject_notice();
    "#).execute(&db).await.unwrap();
    let before: i64 = sqlx::query_scalar("SELECT count(*) FROM notifications")
        .fetch_one(&db)
        .await
        .unwrap();
    assert_eq!(
        open(&h, "asia", "2026-10-03").await.0,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM notifications")
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
    sqlx::query("DROP TRIGGER reject_notice ON notifications")
        .execute(&db)
        .await
        .unwrap();
    let (status, empty) = open(&h, "america", "2026-10-01").await;
    assert_eq!(status, StatusCode::CREATED);
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM notifications WHERE posting_window_id=$1"
        )
        .bind(empty["id"].as_str().unwrap())
        .fetch_one(&db)
        .await
        .unwrap(),
        0
    );
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
