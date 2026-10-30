use super::*;
use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
use std::str::FromStr;

async fn lookups(lookup: &SessionLookups, db: &PgPool) -> Vec<Option<Session>> {
    let mut requests = tokio::task::JoinSet::new();
    // Concurrent duplicate tokens and distinct users
    // must get their own result, including missing/inactive/deleted sessions.
    let sender = lookup.sender.get().unwrap().clone();
    for i in 0..320 {
        let sender = sender.clone();
        requests.spawn(async move {
            let key = format!("token{}", i % 8);
            let (response, result) = oneshot::channel();
            sender
                .send(Lookup {
                    token_hash: key,
                    response,
                })
                .await
                .unwrap();
            (i, result.await.unwrap().unwrap())
        });
    }
    let mut results = vec![None; 320];
    while let Some(result) = requests.join_next().await {
        let (i, session) = result.unwrap();
        results[i] = session;
    }
    // Also cover the public lookup path after the concurrent group.
    assert!(lookup.lookup(db, "missing".into()).await.unwrap().is_none());
    results
}

#[tokio::test]
async fn concurrent_lookups_keep_identity_and_recheck_changes_and_reset() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_session_batch_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let db = PgPoolOptions::new()
        .max_connections(2)
        .connect_with(
            PgConnectOptions::from_str(&dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    sqlx::raw_sql(r#"
        CREATE TABLE users(id text PRIMARY KEY,region text,status text);
        CREATE TABLE login_sessions(token_hash text PRIMARY KEY,user_id text,created_at timestamptz,active bool);
        INSERT INTO users SELECT 'user'||i,'asia','active' FROM generate_series(0,6)i;
        INSERT INTO login_sessions SELECT 'token'||i,'user'||i,now(),true FROM generate_series(0,6)i;
        UPDATE login_sessions SET active=false WHERE user_id='user5';
        UPDATE users SET status='deleted' WHERE id='user6';
    "#).execute(&db).await.unwrap();
    let lookup = SessionLookups::default();
    assert!(
        lookup
            .lookup(&db, "missing".into())
            .await
            .unwrap()
            .is_none()
    );
    for (i, s) in lookups(&lookup, &db).await.into_iter().enumerate() {
        if i % 8 < 5 {
            let s = s.unwrap();
            assert_eq!(s.user_id, format!("user{}", i % 8));
            assert_eq!(s.region, "asia");
        } else {
            assert!(s.is_none());
        }
    }
    // Completed logout, account deletion and region changes must be visible to
    // the very next lookup, even for tokens that previously authenticated.
    sqlx::raw_sql("UPDATE login_sessions SET active=false WHERE user_id='user0'; UPDATE users SET status='deleted' WHERE id='user1'; UPDATE users SET region='europe' WHERE id='user2'; UPDATE login_sessions SET active=true WHERE user_id='user5';")
        .execute(&db).await.unwrap();
    for (i, s) in lookups(&lookup, &db).await.into_iter().enumerate() {
        match i % 8 {
            0 | 1 | 6 | 7 => assert!(s.is_none()),
            n => {
                let s = s.unwrap();
                assert_eq!(s.user_id, format!("user{n}"));
                assert_eq!(s.region, if n == 2 { "europe" } else { "asia" });
            }
        }
    }
    // Fail closed for a SQL error, then recover after the backing table returns.
    sqlx::query("DROP TABLE login_sessions")
        .execute(&db)
        .await
        .unwrap();
    assert!(lookup.lookup(&db, "token2".into()).await.is_err());
    sqlx::raw_sql("CREATE TABLE login_sessions(token_hash text PRIMARY KEY,user_id text,created_at timestamptz,active bool); INSERT INTO login_sessions VALUES ('token2','user3',now(),true);")
        .execute(&db).await.unwrap();
    let current = lookup.lookup(&db, "token2".into()).await.unwrap().unwrap();
    assert_eq!(current.user_id, "user3");
    assert_eq!(current.region, "asia");
    assert!(lookup.lookup(&db, "token3".into()).await.unwrap().is_none());
    drop(lookup);
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
