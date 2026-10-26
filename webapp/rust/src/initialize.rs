use anyhow::anyhow;
use axum::http::StatusCode;
use axum::response::Response;
use serde::Serialize;
use tokio::process::Command;

use crate::api_error::Error;
use crate::response::json;

#[derive(Serialize)]
struct InitializeResponse {
    language: &'static str,
}

pub async fn initialize_handler() -> Result<Response, Error> {
    let status = Command::new("../sql/init.sh").status().await?;
    if !status.success() {
        return Err(anyhow!("{status}").into());
    }

    json(StatusCode::OK, InitializeResponse { language: "rust" })
}
