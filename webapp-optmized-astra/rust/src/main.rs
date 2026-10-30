// 2026-09-23

#[tokio::main]
async fn main() {
    let config = isureal::config_from_env();
    let server = isureal::new_server(config).await;
    let address =
        std::env::var("ISUREAL_LISTEN_ADDRESS").unwrap_or_else(|_| "0.0.0.0:8080".to_owned());
    if let Err(err) = isureal::start(server, &address).await {
        panic!("failed to start server: {err}");
    }
}
