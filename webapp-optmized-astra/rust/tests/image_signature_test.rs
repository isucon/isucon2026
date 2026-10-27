use std::sync::Arc;

use axum::Router;
use axum::body::Body;
use axum::http::{Request, StatusCode, header};
use axum::routing::get;
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use chrono::Utc;
use isureal::image_api::get_image_handler;
use isureal::image_signature::{image_signature, image_signing_key, signed_image_url};
use isureal::ip_ban::IpBanList;
use isureal::user::User;
use isureal::{HandlerContext, config_from_env};
use sha2::{Digest, Sha256};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[test]
fn image_signature_vector() {
    // Python の hashlib/hmac で1000回反復して求めた値と照合する（Go の TestImageSignatureVector と同じ値）。
    let key = Sha256::digest(b"test-secret");
    let got = URL_SAFE_NO_PAD.encode(image_signature(
        &key,
        "550e8400-e29b-41d4-a716-446655440000",
        1790553600,
    ));
    assert_eq!(got, "qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84");
    let decoded = image_signing_key(&hex::encode(key)).unwrap();
    assert_eq!(decoded, key.to_vec());
    for invalid in ["", "hash", "abcd"] {
        assert!(
            image_signing_key(invalid).is_err(),
            "accepted invalid stored key {invalid:?}"
        );
    }
}

#[test]
fn signed_image_url_lifetime() {
    let key = Sha256::digest(b"test-secret");
    let user = User {
        id: "owner".into(),
        region: "asia".into(),
        image_key: key.to_vec(),
        ..User::default()
    };
    let before = Utc::now().timestamp();
    let link = signed_image_url(&user, "image-id");
    let after = Utc::now().timestamp();
    let (path, query) = link.split_once('?').unwrap();
    assert_eq!(path, "/api/images/image-id");
    let params: Vec<(&str, &str)> = query
        .split('&')
        .map(|pair| pair.split_once('=').unwrap())
        .collect();
    assert_eq!(params.len(), 2, "unexpected URL: {link}");
    assert_eq!(params[0].0, "exp");
    let exp: i64 = params[0].1.parse().unwrap();
    assert!(
        exp >= before + 86400 && exp <= after + 86400,
        "expiry = {exp}, want issuance + 24h"
    );
    assert_eq!(
        params[1],
        (
            "sig",
            URL_SAFE_NO_PAD
                .encode(image_signature(&key, "image-id", exp))
                .as_str()
        )
    );
}

#[tokio::test]
async fn image_rejects_malformed_and_expired_url() {
    // 不正な URL は DB にアクセスする前に拒否する（接続できない DB を渡す）。
    let db = PgPoolOptions::new()
        .connect_lazy("postgres://isureal:isureal@127.0.0.1:1/isureal")
        .unwrap();
    let h = Arc::new(HandlerContext {
        db,
        config: config_from_env(),
        ip_bans: IpBanList::default(),
    });
    let app = Router::new()
        .route("/api/images/{imageId}", get(get_image_handler))
        .with_state(h);
    let now = Utc::now().timestamp();
    let key = Sha256::digest(b"test-secret");
    let sign = |exp: i64| URL_SAFE_NO_PAD.encode(image_signature(&key, "image", exp));
    let future = (now + 86400).to_string();
    let cases = [
        ("missing", String::new()),
        ("missing signature", format!("exp={future}")),
        ("missing expiry", format!("sig={}", sign(now + 86400))),
        ("invalid expiry", "exp=tomorrow&sig=x".to_owned()),
        ("overflow", "exp=999999999999999999999999&sig=x".to_owned()),
        ("leading zero", format!("exp=0{future}&sig=x")),
        ("invalid signature", format!("exp={future}&sig=***")),
        ("short signature", format!("exp={future}&sig=YQ")),
        (
            "newline in signature",
            format!("exp={future}&sig={}%0A", sign(now + 86400)),
        ),
        (
            "duplicate expiry",
            format!("exp={future}&exp={future}&sig=x"),
        ),
        ("duplicate signature", format!("exp={future}&sig=x&sig=y")),
        ("malformed query", format!("exp={future}&sig=%zz")),
        ("expired", format!("exp={}&sig={}", now - 1, sign(now - 1))),
        ("expiry boundary", format!("exp={now}&sig={}", sign(now))),
    ];
    for (name, query) in cases {
        let request = Request::get(format!("/api/images/image?{query}"))
            .body(Body::empty())
            .unwrap();
        let response = app.clone().oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::FORBIDDEN, "{name}");
        assert_eq!(
            response.headers().get(header::CACHE_CONTROL).unwrap(),
            "no-store",
            "{name}"
        );
    }
}
