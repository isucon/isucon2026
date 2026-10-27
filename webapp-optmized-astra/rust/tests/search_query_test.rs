use axum::{
    Extension, Router,
    body::Body,
    http::{Request, StatusCode},
    routing::get,
};
use chrono::Utc;
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env,
    session::Session,
    user::{USER_COLUMNS, User},
    user_read_api::search_users_handler,
};
use serde_json::Value;
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{str::FromStr, sync::Arc};
use tower::ServiceExt;
use unicode_normalization::UnicodeNormalization;

fn normalized(text: &str) -> String {
    caseless::default_case_fold_str(&text.nfc().collect::<String>())
        .nfc()
        .collect()
}
fn escaped(text: &str) -> String {
    percent_encoding::utf8_percent_encode(text, percent_encoding::NON_ALPHANUMERIC).to_string()
}
async fn search(app: Router, q: &str, before: &(String, String)) -> Value {
    let uri = format!(
        "/api/users?q={}&beforeUsername={}&beforeId={}",
        escaped(q),
        escaped(&before.0),
        escaped(&before.1)
    );
    let response = app
        .oneshot(Request::get(uri).body(Body::empty()).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    assert_eq!(status, StatusCode::OK, "{}", String::from_utf8_lossy(&body));
    serde_json::from_slice(&body).unwrap()
}
async fn compare(app: Router, db: &PgPool, q: &str) {
    let normalized_query = normalized(q);
    let mut before = (String::new(), String::new());
    for _ in 0..6 {
        let users:Vec<User>=sqlx::query_as(&format!("SELECT {} FROM users WHERE status='active' AND id<>'viewer' AND ($1='' OR (username,id)>($1,$2)) ORDER BY username,id",USER_COLUMNS.join(","))).bind(&before.0).bind(&before.1).fetch_all(db).await.unwrap();
        let blocked = isureal::post::get_blocked_user_ids(db, "viewer")
            .await
            .unwrap();
        let expected: Vec<User> = users
            .into_iter()
            .filter(|u| {
                !blocked.contains(&u.id)
                    && (normalized(&u.username).contains(&normalized_query)
                        || normalized(&u.display_name).contains(&normalized_query))
            })
            .take(51)
            .collect();
        let result = search(app.clone(), q, &before).await;
        let items = result["items"].as_array().unwrap();
        assert_eq!(result["hasMore"], expected.len() > 50, "{q}");
        assert_eq!(items.len(), expected.len().min(50), "{q}");
        for (got, want) in items.iter().zip(&expected) {
            assert_eq!(got["id"], want.id, "{q}");
            assert_eq!(got["username"], want.username);
            assert_eq!(got["displayName"], want.display_name);
            assert_eq!(got["accountType"], want.account_type);
            assert_eq!(
                got["bio"],
                if want.account_type == "certified_public" {
                    want.bio.as_str()
                } else {
                    ""
                }
            );
        }
        if !result["hasMore"].as_bool().unwrap() {
            return;
        }
        let last = items.last().unwrap();
        before = (
            last["username"].as_str().unwrap().into(),
            last["id"].as_str().unwrap().into(),
        );
    }
    panic!("pagination did not finish");
}
#[tokio::test]
async fn search_preserves_unicode_pagination_and_fresh_account_visibility() {
    let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
        return;
    };
    let schema = format!("astra_search_{}", uuid::Uuid::new_v4().simple());
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
    sqlx::raw_sql("CREATE TABLE users(id text PRIMARY KEY,username text,secret_key_hash text,display_name text,bio text,profile_image_id text,region text,account_type text,status text); CREATE TABLE block_relations(id text PRIMARY KEY,blocker_id text,blocked_id text);").execute(&db).await.unwrap();
    sqlx::query("INSERT INTO users SELECT 'user-'||lpad(i::text,4,'0'), CASE WHEN i<55 THEN 'page' ELSE 'page-'||lpad(i::text,4,'0') END, repeat('ab',32), CASE WHEN i%3=0 THEN 'Café Straße '||i ELSE '利用者 '||i END,'private biography',NULL,'asia',CASE WHEN i%5=0 THEN 'certified_public' ELSE 'regular' END,CASE WHEN i%17=0 THEN 'deleted' ELSE 'active' END FROM generate_series(1,175)i").execute(&db).await.unwrap();
    sqlx::query("INSERT INTO users VALUES ('viewer','page-viewer',repeat('ab',32),'Café Straße','viewer',NULL,'asia','regular','active')").execute(&db).await.unwrap();
    let h = Arc::new(HandlerContext {
        db: db.clone(),
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        read_fence: None,
        image_store: None,
    });
    let app = Router::new()
        .route("/api/users", get(search_users_handler))
        .with_state(h)
        .layer(Extension(Session {
            region: "asia".into(),
            token: String::new(),
            user_id: "viewer".into(),
            token_hash: String::new(),
            created_at: Utc::now(),
        }));
    for stage in 0..4 {
        match stage {
            1 => {
                sqlx::query("INSERT INTO block_relations VALUES ('out','viewer','user-0003'),('in','user-0006','viewer')").execute(&db).await.unwrap();
            }
            2 => {
                sqlx::raw_sql("UPDATE users SET username='renamed', display_name='İstanbul Σ Straße CAFÉ %_\\',status='active',account_type='certified_public',bio='new biography' WHERE id='user-0003'; UPDATE users SET status='deleted' WHERE id='user-0009'").execute(&db).await.unwrap();
            }
            3 => {
                sqlx::query("DELETE FROM block_relations")
                    .execute(&db)
                    .await
                    .unwrap();
            }
            _ => {}
        }
        for q in [
            "PAGE",
            "CAFE\u{301}",
            "STRASSE",
            "利用者",
            "renamed",
            "i\u{307}",
            "σ",
            "%",
            "_",
            "\\",
            "no match",
        ] {
            compare(app.clone(), &db, q).await;
        }
    }
    let renamed = search(app.clone(), "renamed", &(String::new(), String::new())).await;
    assert_eq!(renamed["items"][0]["id"], "user-0003");
    assert_eq!(renamed["items"][0]["bio"], "new biography");
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
