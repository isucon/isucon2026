//! Read an authorized immutable archive range from an existing private VM.
use std::{io, net::SocketAddr, time::Duration};

use hyper_client::{Body, Client, Request, StatusCode, body::HttpBody, client::HttpConnector};

use crate::archive_store::{ArchiveReference, MAX_ARCHIVE_BYTES};

fn invalid(message: &'static str) -> sqlx::Error {
    sqlx::Error::Protocol(message.into())
}

pub(crate) async fn read(
    client: &Client<HttpConnector, Body>,
    peer: SocketAddr,
    reference: &ArchiveReference,
) -> Result<Vec<u8>, sqlx::Error> {
    let last = reference.offset + reference.length as u64 - 1;
    let request = Request::builder()
        .uri(format!(
            "http://{peer}/internal/astra/packs/{}",
            reference.archive
        ))
        .header("Range", format!("bytes={}-{last}", reference.offset))
        .header("Accept-Encoding", "identity")
        .body(Body::empty())
        .map_err(|_| invalid("invalid archive peer request"))?;
    tokio::time::timeout(Duration::from_secs(10), async {
        let mut response = client
            .request(request)
            .await
            .map_err(|error| sqlx::Error::Io(io::Error::other(error)))?;
        if response.status() == StatusCode::NOT_FOUND {
            return Err(sqlx::Error::RowNotFound);
        }
        if response.status() != StatusCode::PARTIAL_CONTENT {
            return Err(invalid("archive peer did not return a partial response"));
        }
        let prefix = format!("bytes {}-{last}/", reference.offset);
        let total = response
            .headers()
            .get("Content-Range")
            .and_then(|header| header.to_str().ok())
            .and_then(|range| range.strip_prefix(&prefix))
            .and_then(|total| total.parse::<u64>().ok());
        if !total.is_some_and(|total| last < total && total <= MAX_ARCHIVE_BYTES) {
            return Err(invalid("archive peer returned an invalid range"));
        }
        let mut bytes = Vec::with_capacity(reference.length);
        while let Some(chunk) = response.body_mut().data().await {
            let chunk = chunk.map_err(|error| sqlx::Error::Io(io::Error::other(error)))?;
            if bytes.len() + chunk.len() > reference.length {
                return Err(invalid("archive peer returned too many bytes"));
            }
            bytes.extend_from_slice(&chunk);
        }
        if bytes.len() != reference.length {
            return Err(invalid("archive peer returned a truncated image"));
        }
        Ok(bytes)
    })
    .await
    .map_err(|_| invalid("archive peer read timed out"))?
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{Router, http::HeaderMap, response::IntoResponse, routing::get};

    #[tokio::test]
    async fn reads_exact_ranges_and_rejects_incorrect_peer_responses() {
        for (status, range, body, succeeds) in [
            (206, "bytes 2-4/8", "cde", true),
            (200, "bytes 2-4/8", "cde", false),
            (404, "", "", false),
            (416, "bytes */8", "", false),
            (206, "bytes 3-5/8", "cde", false),
            (206, "bytes 2-4/4", "cde", false),
            (206, "bytes 2-4/8589934593", "cde", false),
            (206, "bytes 2-4/*", "cde", false),
            (206, "bytes 2-4/8", "cd", false),
            (206, "bytes 2-4/8", "cdef", false),
        ] {
            let app = Router::new().route(
                "/internal/astra/packs/{archive}",
                get(move |headers: HeaderMap| async move {
                    assert_eq!(headers["Range"], "bytes=2-4");
                    assert_eq!(headers["Accept-Encoding"], "identity");
                    (
                        axum::http::StatusCode::from_u16(status).unwrap(),
                        [("Content-Range", range)],
                        body,
                    )
                        .into_response()
                }),
            );
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let peer = listener.local_addr().unwrap();
            let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
            let reference =
                ArchiveReference::parse(&format!("pack:{}:2:3", uuid::Uuid::new_v4().simple()))
                    .unwrap();
            let result = read(&Client::new(), peer, &reference).await;
            assert_eq!(
                result.is_ok(),
                succeeds,
                "status={status} range={range} body={body}"
            );
            if succeeds {
                assert_eq!(result.unwrap(), b"cde");
            }
            server.abort();
        }
    }
}
