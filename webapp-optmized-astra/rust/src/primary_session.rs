//! Route sessions of a fixed set of users to their authoritative primary.
//! Only their successful login/logout responses can omit the replica barrier.
use axum::{http::Method, response::Response};
use sqlx::PgPool;

#[derive(Clone)]
pub struct PrimarySessionPolicy {
    pub primary: PgPool,
    pub local_is_primary: bool,
    pub local_writes: bool,
}

impl PrimarySessionPolicy {
    pub fn new(primary: PgPool, local_is_primary: bool, local_writes: bool) -> Self {
        Self {
            primary,
            local_is_primary,
            local_writes,
        }
    }

    pub fn local_mutation_user(
        &self,
        method: &Method,
        path: &str,
        response: &Response,
    ) -> Option<String> {
        if !self.local_writes || *method != Method::POST || !response.status().is_success() {
            return None;
        }
        let user_id = match path {
            "/api/auth/login" => response
                .extensions()
                .get::<crate::session_cache::ChangedSessionUser>()
                .map(|u| u.0.as_str()),
            "/api/auth/logout" => response
                .extensions()
                .get::<crate::session::Session>()
                .map(|s| s.user_id.as_str()),
            _ => None,
        };
        user_id.map(str::to_owned)
    }

    pub async fn is_primary_authority(&self, user_id: &str) -> bool {
        // Failure falls back to the existing all-replica barrier. This field is
        // immutable, including during concurrent visible-region changes.
        sqlx::query_scalar::<_, bool>("SELECT auth_home_region='asia' FROM users WHERE id=$1")
            .bind(user_id)
            .fetch_optional(&self.primary)
            .await
            .ok()
            .flatten()
            .unwrap_or(false)
    }
}
