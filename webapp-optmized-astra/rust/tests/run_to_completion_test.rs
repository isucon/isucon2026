use std::env;
use std::future::poll_fn;
use std::pin::pin;
use std::str::FromStr;
use std::task::Poll;

use axum::Router;
use axum::body::Body;
use axum::extract::State;
use axum::http::{Request, StatusCode};
use axum::middleware::from_fn;
use axum::routing::post;
use isureal::Error;
use isureal::server::run_to_completion;
use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
use sqlx::{Connection, PgConnection, PgPool};
use tower::ServiceExt;

async fn increment_handler(State(db): State<PgPool>) -> Result<StatusCode, Error> {
    let mut tx = db.begin().await?;
    sqlx::query("UPDATE run_to_completion_test SET n = n + 1 WHERE id = 1")
        .execute(&mut *tx)
        .await?;
    tx.commit().await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn backend_state(monitor: &mut PgConnection, application_name: &str) -> Vec<String> {
    sqlx::query_scalar("SELECT state FROM pg_stat_activity WHERE application_name = $1")
        .bind(application_name)
        .fetch_all(monitor)
        .await
        .unwrap()
}

#[tokio::test]
async fn cancelled_request_leaves_no_open_transaction() {
    // Go の *_test.go と同じく、ISUREAL_SESSION_TEST_DSN がなければ何もしない。
    let Ok(dsn) = env::var("ISUREAL_SESSION_TEST_DSN") else {
        eprintln!("ISUREAL_SESSION_TEST_DSN is unset");
        return;
    };
    let application_name = format!("isureal-test-{}", uuid::Uuid::new_v4());
    let options = PgConnectOptions::from_str(&dsn)
        .unwrap()
        .application_name(&application_name);
    // 接続を 1 本にして、打ち切られたリクエストが使った接続をそのまま調べる。
    // test_before_acquire を切ると、最初の poll で BEGIN の応答待ちまで進む。
    let db = PgPoolOptions::new()
        .max_connections(1)
        .test_before_acquire(false)
        .connect_with(options)
        .await
        .unwrap();
    sqlx::query(
        "CREATE TEMPORARY TABLE run_to_completion_test (id int PRIMARY KEY, n int NOT NULL)",
    )
    .execute(&db)
    .await
    .unwrap();
    sqlx::query("INSERT INTO run_to_completion_test VALUES (1, 0)")
        .execute(&db)
        .await
        .unwrap();
    let mut monitor = PgConnection::connect(&dsn).await.unwrap();

    let app = Router::new()
        .route("/increment", post(increment_handler))
        .layer(from_fn(run_to_completion))
        .with_state(db.clone());

    // poll を 1〜5 回で打ち切る（接続の取得、BEGIN、UPDATE、COMMIT のどの待ちで止めても同じ）。
    let requests = 5;
    for polls in 1..=requests {
        let request = Request::post("/increment").body(Body::empty()).unwrap();
        let finished = {
            let mut response = pin!(app.clone().oneshot(request));
            let mut remaining = polls;
            poll_fn(|cx| match response.as_mut().poll(cx) {
                Poll::Ready(response) => Poll::Ready(Some(response.unwrap().status())),
                Poll::Pending if remaining > 1 => {
                    remaining -= 1;
                    Poll::Pending
                }
                Poll::Pending => Poll::Ready(None),
            })
            .await
        };

        // 接続は 1 本なので、取得できたときには打ち切られたリクエストの処理が終わっている。
        let conn = db.acquire().await.unwrap();
        let states = backend_state(&mut monitor, &application_name).await;
        assert_eq!(
            states,
            vec!["idle".to_owned()],
            "polls = {polls}, finished = {finished:?}"
        );
        drop(conn);
    }

    let n: i32 = sqlx::query_scalar("SELECT n FROM run_to_completion_test WHERE id = 1")
        .fetch_one(&db)
        .await
        .unwrap();
    assert_eq!(n, requests, "every request should run to completion");
}
