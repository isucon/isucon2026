use axum::{
    Extension, Router,
    body::Body,
    http::{Request, StatusCode},
    middleware::from_fn_with_state,
    response::IntoResponse,
    routing::{get, post},
};
use futures_util::FutureExt;
use http_body_util::BodyExt;
use isureal::{
    HandlerContext, config_from_env,
    primary_session::PrimarySessionPolicy,
    read_fence::ReadFence,
    session::{Session, create_login_session, memory_session_middleware, session_middleware},
    user_api::{login_handler, logout_handler},
    write_fence::{WriteFence, write_fence_middleware},
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{
    PgPool,
    postgres::{PgConnectOptions, PgPoolOptions},
};
use std::{panic::AssertUnwindSafe, str::FromStr, sync::Arc, time::Duration};
use tower::ServiceExt;

async fn request(
    app: &Router,
    method: &str,
    path: &str,
    token: &str,
    body: Value,
) -> (StatusCode, Value) {
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header("content-type", "application/json")
                .header("authorization", format!("Bearer {token}"))
                .body(Body::from(body.to_string()))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (
        status,
        if bytes.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&bytes).unwrap()
        },
    )
}
async fn caught_up(primary: &PgPool, replica: &PgPool) {
    let lsn: String = sqlx::query_scalar("SELECT pg_current_wal_insert_lsn()::text")
        .fetch_one(primary)
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_secs(5), async {
        while !sqlx::query_scalar::<_, bool>("SELECT pg_last_wal_replay_lsn()>=$1::pg_lsn")
            .bind(&lsn)
            .fetch_one(replica)
            .await
            .unwrap()
        {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}
fn context(db: PgPool) -> Arc<HandlerContext> {
    Arc::new(HandlerContext {
        db,
        config: config_from_env(),
        ip_bans: Default::default(),
        notifications: Default::default(),
        notification_batch_writes: false,
        read_fence: None,
        image_store: None,
    })
}
#[tokio::test]
async fn primary_sessions_remain_fresh_while_replay_is_paused_and_other_writes_still_wait() {
    let (Ok(dsn), Ok(replica_dsn), Ok(second_dsn)) = (
        std::env::var("ISUREAL_SESSION_TEST_DSN"),
        std::env::var("ISUREAL_FENCE_REPLICA_DSN"),
        std::env::var("ISUREAL_SECOND_REPLICA_DSN"),
    ) else {
        return;
    };
    let admin = PgPool::connect(&dsn).await.unwrap();
    let schema = format!("astra_authority_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let options = PgConnectOptions::from_str(&dsn).unwrap().options([
        ("search_path", schema.as_str()),
        ("synchronous_commit", "local"),
    ]);
    let db = PgPoolOptions::new()
        .max_connections(8)
        .connect_with(options)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE users(id text PRIMARY KEY, username text UNIQUE,secret_key_hash text,display_name text DEFAULT 'User',bio text DEFAULT '',profile_image_id text,region text,account_type text DEFAULT 'regular',status text DEFAULT 'active'); CREATE TABLE login_sessions(id text PRIMARY KEY,user_id text,token_hash text UNIQUE,created_at timestamptz DEFAULT clock_timestamp(),active boolean DEFAULT true,invalidated_at timestamptz,reason text); CREATE UNIQUE INDEX active_user ON login_sessions(user_id) WHERE active;")
        .execute(&db).await.unwrap();
    for (id, region) in [("userasia", "asia"), ("usereurope", "europe")] {
        sqlx::query("INSERT INTO users(id,username,secret_key_hash,region) VALUES($1,$1,$2,$3)")
            .bind(id)
            .bind(hex::encode(Sha256::digest(b"secret")))
            .bind(region)
            .execute(&db)
            .await
            .unwrap();
    }
    sqlx::raw_sql("CREATE TABLE initial_users AS SELECT * FROM users")
        .execute(&db)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../../sql/023-session-authority.sql"))
        .execute(&db)
        .await
        .unwrap();
    let old = create_login_session(&db, "userasia").await.unwrap();
    let europe = create_login_session(&db, "usereurope").await.unwrap();
    let replica_admin = PgPool::connect(&replica_dsn).await.unwrap();
    let second = PgPool::connect(&second_dsn).await.unwrap();
    caught_up(&db, &replica_admin).await;
    caught_up(&db, &second).await;
    let replica = PgPoolOptions::new()
        .max_connections(2)
        .connect_with(
            PgConnectOptions::from_str(&replica_dsn)
                .unwrap()
                .options([("search_path", schema.as_str())]),
        )
        .await
        .unwrap();
    let primary_h = context(db.clone());
    let replica_h = context(replica.clone());
    let session_h = isureal::db_pool::session_context(&primary_h, 1)
        .await
        .unwrap();
    let policy = PrimarySessionPolicy::new(session_h.db.clone(), true, true);
    let writer = Router::new()
        .route("/api/auth/login", post(login_handler))
        .route(
            "/api/auth/logout",
            post(logout_handler).route_layer(from_fn_with_state(
                (session_h.clone(), true),
                session_middleware,
            )),
        )
        .route(
            "/api/other",
            post(|| async {
                let mut r = StatusCode::OK.into_response();
                r.extensions_mut()
                    .insert(isureal::session_cache::ChangedSessionUser(
                        "userasia".into(),
                    ));
                r
            }),
        )
        .layer(from_fn_with_state(
            Some(WriteFence::new(vec![
                ReadFence::new(db.clone(), replica_admin.clone(), Duration::from_secs(5)),
                ReadFence::new(db.clone(), second.clone(), Duration::from_secs(5)),
            ])),
            write_fence_middleware,
        ))
        .layer(Extension(policy.clone()))
        .with_state(session_h.clone());
    let reader = Router::new()
        .route(
            "/probe",
            get(|Extension(session): Extension<Session>| async move {
                axum::Json(json!({"id":session.user_id,"region":session.region}))
            }),
        )
        .route_layer(from_fn_with_state(
            (replica_h, true),
            memory_session_middleware,
        ))
        .layer(Extension(PrimarySessionPolicy::new(
            session_h.db.clone(),
            false,
            true,
        )));
    assert_eq!(
        request(&reader, "GET", "/probe", &old.token, Value::Null)
            .await
            .0,
        StatusCode::OK
    );
    let mut ordinary_held = Vec::new();
    for _ in 0..8 {
        ordinary_held.push(db.acquire().await.unwrap());
    }
    assert_eq!(
        tokio::time::timeout(
            Duration::from_millis(500),
            request(&writer, "POST", "/api/auth/logout", &old.token, Value::Null)
        )
        .await
        .unwrap()
        .0,
        StatusCode::NO_CONTENT
    );
    let initial_login = tokio::time::timeout(
        Duration::from_millis(500),
        request(
            &writer,
            "POST",
            "/api/auth/login",
            "",
            json!({"username":"userasia","secretKey":"secret"}),
        ),
    )
    .await
    .unwrap();
    assert_eq!(initial_login.0, StatusCode::OK);
    drop(ordinary_held);
    let old_token = initial_login.1["token"].as_str().unwrap();
    let old = Session {
        token: old_token.to_owned(),
        token_hash: hex::encode(Sha256::digest(old_token.as_bytes())),
        ..old
    };
    caught_up(&db, &replica_admin).await;
    sqlx::query("SELECT pg_wal_replay_pause()")
        .execute(&replica_admin)
        .await
        .unwrap();
    let outcome=AssertUnwindSafe(async {
        tokio::time::timeout(Duration::from_secs(2),async {
            while sqlx::query_scalar::<_,String>("SELECT pg_get_wal_replay_pause_state()").fetch_one(&replica_admin).await.unwrap()!="paused"{tokio::time::sleep(Duration::from_millis(5)).await;}
        }).await.unwrap();
        let result=tokio::time::timeout(Duration::from_millis(500),request(&writer,"POST","/api/auth/logout",&old.token,Value::Null)).await.unwrap();
        assert_eq!(result.0,StatusCode::NO_CONTENT);
        assert!(sqlx::query_scalar::<_,bool>("SELECT active FROM login_sessions WHERE token_hash=$1").bind(&old.token_hash).fetch_one(&replica).await.unwrap(),"replica still holds the logged-out session");
        assert_eq!(request(&reader,"GET","/probe",&old.token,Value::Null).await.0,StatusCode::UNAUTHORIZED);
        let login=json!({"username":"userasia","secretKey":"secret"});
        let response=tokio::time::timeout(Duration::from_millis(500),request(&writer,"POST","/api/auth/login","",login.clone())).await.unwrap();
        assert_eq!(response.0,StatusCode::OK);
        let token=response.1["token"].as_str().unwrap();
        assert_eq!(sqlx::query_scalar::<_,i64>("SELECT count(*) FROM login_sessions WHERE token_hash=$1").bind(hex::encode(Sha256::digest(token.as_bytes()))).fetch_one(&replica).await.unwrap(),0,"new session has not replayed");
        assert_eq!(request(&reader,"GET","/probe",token,Value::Null).await.0,StatusCode::OK);
        let (a,b)=tokio::join!(request(&writer,"POST","/api/auth/login","",login.clone()),request(&writer,"POST","/api/auth/login","",login));
        assert_eq!(a.0,StatusCode::OK);assert_eq!(b.0,StatusCode::OK);
        let mut active=0;
        for token in [token,a.1["token"].as_str().unwrap(),b.1["token"].as_str().unwrap()] {
            active+=usize::from(request(&reader,"GET","/probe",token,Value::Null).await.0==StatusCode::OK);
        }
        assert_eq!(active,1,"only the final concurrent login is valid");
        let current_hash:String=sqlx::query_scalar("SELECT token_hash FROM login_sessions WHERE user_id='userasia' AND active").fetch_one(&db).await.unwrap();
        let current=[&a.1,&b.1].into_iter().find_map(|v|{let t=v["token"].as_str().unwrap();(hex::encode(Sha256::digest(t.as_bytes()))==current_hash).then_some(t)}).unwrap();
        sqlx::query("UPDATE users SET region='europe',auth_home_region='europe' WHERE id='userasia'").execute(&db).await.unwrap();
        assert!(policy.is_primary_authority("userasia").await);
        assert_eq!(request(&reader,"GET","/probe",current,Value::Null).await.1["region"],"europe");
        sqlx::query("UPDATE users SET status='deleted' WHERE id='userasia'").execute(&db).await.unwrap();
        assert_eq!(request(&reader,"GET","/probe",current,Value::Null).await.0,StatusCode::UNAUTHORIZED);
        sqlx::query("UPDATE users SET region='asia' WHERE id='usereurope'").execute(&db).await.unwrap();
        assert!(!policy.is_primary_authority("usereurope").await,"moving to Asia does not change authority");
        let held=session_h.db.acquire().await.unwrap();
        assert_eq!(tokio::time::timeout(Duration::from_millis(500),request(&reader,"GET","/probe",&europe.token,Value::Null)).await.unwrap().0,StatusCode::OK,"non-Asia authority still uses local SQL");
        drop(held);
        sqlx::query("INSERT INTO users(id,username,secret_key_hash,region) VALUES('new','new','unused','america')").execute(&db).await.unwrap();
        assert_eq!(sqlx::query_scalar::<_,String>("SELECT auth_home_region FROM users WHERE id='new'").fetch_one(&db).await.unwrap(),"america");
        let other_app=writer.clone();
        let other=tokio::spawn(async move{request(&other_app,"POST","/api/other","",Value::Null).await});
        let eu_app=writer.clone();
        let eu=tokio::spawn(async move{request(&eu_app,"POST","/api/auth/login","",json!({"username":"usereurope","secretKey":"secret"})).await});
        tokio::time::sleep(Duration::from_millis(100)).await;
        assert!(!other.is_finished(),"non-session writes retain their replay barrier");
        assert!(!eu.is_finished(),"other session authorities retain their replay barrier");
        (other,eu)
    }).catch_unwind().await;
    sqlx::query("SELECT pg_wal_replay_resume()")
        .execute(&replica_admin)
        .await
        .unwrap();
    let (other, eu) = outcome.unwrap();
    assert_eq!(other.await.unwrap().0, StatusCode::OK);
    let eu = eu.await.unwrap();
    assert_eq!(eu.0, StatusCode::OK);
    assert_eq!(
        request(
            &reader,
            "GET",
            "/probe",
            eu.1["token"].as_str().unwrap(),
            Value::Null
        )
        .await
        .0,
        StatusCode::OK
    );
    let mut disabled = policy.clone();
    disabled.local_writes = false;
    let mut response = StatusCode::OK.into_response();
    response
        .extensions_mut()
        .insert(isureal::session_cache::ChangedSessionUser(
            "userasia".into(),
        ));
    assert!(
        disabled
            .local_mutation_user(&axum::http::Method::POST, "/api/auth/login", &response)
            .is_none()
    );
    assert!(
        policy
            .local_mutation_user(&axum::http::Method::GET, "/api/auth/login", &response)
            .is_none()
    );
    *response.status_mut() = StatusCode::UNAUTHORIZED;
    assert!(
        policy
            .local_mutation_user(&axum::http::Method::POST, "/api/auth/login", &response)
            .is_none()
    );
    drop(reader);
    drop(writer);
    replica.close().await;
    db.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
    replica_admin.close().await;
    second.close().await;
}
