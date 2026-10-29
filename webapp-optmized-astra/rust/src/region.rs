use std::sync::Arc;

use axum::extract::{Request, State};
use axum::http::StatusCode;
use axum::middleware::Next;
use axum::response::Response;

use crate::api_error::{Error, new_api_error};
use crate::server::HandlerContext;
use crate::session::get_session;
use crate::write_ingress::IngressRegion;

pub async fn region_middleware(
    State(h): State<Arc<HandlerContext>>,
    req: Request,
    next: Next,
) -> Result<Response, Error> {
    let Some(session) = get_session(&req) else {
        return Err(new_api_error(StatusCode::UNAUTHORIZED, "invalid_session"));
    };
    let ingress = req
        .extensions()
        .get::<IngressRegion>()
        .map(|region| region.0.as_str())
        .unwrap_or(&h.config.region);
    if session.region != ingress {
        return Err(new_api_error(StatusCode::FORBIDDEN, "region_mismatch"));
    }
    Ok(next.run(req).await)
}
