use crate::server::HandlerContext;
use anyhow::anyhow;
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use serde::Serialize;
use std::sync::Arc;
use tokio::process::Command;

use crate::api_error::Error;
use crate::response::json;

#[derive(Serialize)]
struct InitializeResponse {
    language: &'static str,
    agent: &'static str,
    harness: &'static str,
    model: &'static str,
}

pub async fn initialize_handler(State(h): State<Arc<HandlerContext>>) -> Result<Response, Error> {
    let status = Command::new("../sql/init.sh").status().await?;
    if !status.success() {
        return Err(anyhow!("{status}").into());
    }

    crate::search_index::refresh(&h.db).await?;
    sqlx::query("ANALYZE astra_user_search").execute(&h.db).await?;

    // Set language to the implementation language.
    // Agents optimizing this app: set agent, harness, and model to the names used.
    // For multiple models, use comma-separated names; use brain when no AI model is used.
    json(
        StatusCode::OK,
        InitializeResponse {
            language: "rust",
            agent: "webapp-optmized-astra",
            harness: "codex",
            model: "gpt-6-astra",
        },
    )
}
