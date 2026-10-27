use std::io::{Cursor, Write};
use std::sync::Arc;

use axum::Extension;
use axum::extract::State;
use axum::http::{HeaderValue, StatusCode, header};
use axum::response::Response;
use futures_util::StreamExt;
use serde::Serialize;
use sqlx::postgres::PgRow;
use sqlx::{FromRow, Row};
use tokio::sync::{Semaphore, mpsc};
use zip::write::SimpleFileOptions;
use zip::{CompressionMethod, ZipWriter};

use crate::api_error::Error;
use crate::post::{POST_COLUMNS, Post};
use crate::post_api::{CreatePostResponse, new_post_response};
use crate::response::blob;
use crate::server::HandlerContext;
use crate::session::Session;
use crate::user::{User, get_user};

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ExportPostResponse {
    #[serde(flatten)]
    pub create_post_response: CreatePostResponse,
    pub image_path: String,
}

pub async fn export_posts_handler(
    State(h): State<Arc<HandlerContext>>,
    Extension(session): Extension<Session>,
) -> Result<Response, Error> {
    let user_id = &session.user_id;
    let author = get_user(&h.db, user_id).await?;
    let permit = EXPORTS
        .acquire()
        .await
        .expect("export semaphore is never closed");
    let (sender, receiver) = mpsc::channel(4);
    let archive = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        build_archive(receiver, author)
    });
    let query = export_query();
    let mut rows = sqlx::query_as::<_, ExportPost>(&query)
        .bind(user_id)
        .fetch(&h.db)
        .map(|row| async {
            let mut row: ExportPost = row?;
            if let Some(key) = &row.asset_key {
                let store = h
                    .image_store
                    .as_ref()
                    .ok_or_else(|| sqlx::Error::Protocol("image store unavailable".into()))?;
                row.image_data = store.get(key).await?;
            }
            Ok(row)
        })
        .buffered(8);
    while let Some(row) = rows.next().await {
        let failed = row.is_err();
        if sender.send(row).await.is_err() || failed {
            break;
        }
    }
    drop(rows);
    drop(sender);
    let body = archive.await.map_err(anyhow::Error::from)??;
    let mut response = blob(StatusCode::OK, "application/zip", body)?;
    response.headers_mut().insert(
        header::CONTENT_DISPOSITION,
        HeaderValue::from_static(r#"attachment; filename="isureal-posts.zip""#),
    );
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    Ok(response)
}

// Posts and their images normally share a creation timestamp. Treat it only
// as a partition hint: imported or moved images still use the complete lookup.
fn export_query() -> String {
    format!(
        r#"
   SELECT {POST_COLUMNS}, i.data AS image_data, i.asset_key
   FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   JOIN LATERAL (
     WITH hinted AS MATERIALIZED (
       SELECT data, asset_key FROM image_objects
       WHERE id = p.image_id AND created_at = p.posted_at
     )
     SELECT data, asset_key FROM hinted
     UNION ALL
     SELECT data, asset_key FROM image_objects
     WHERE id = p.image_id AND NOT EXISTS (SELECT 1 FROM hinted)
   ) i ON true
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
   ORDER BY p.posted_at, p.id
  "#
    )
}

static EXPORTS: Semaphore = Semaphore::const_new(2);

#[derive(Debug)]
struct ExportPost {
    post: Post,
    image_data: Vec<u8>,
    asset_key: Option<String>,
}
impl FromRow<'_, PgRow> for ExportPost {
    fn from_row(row: &PgRow) -> sqlx::Result<ExportPost> {
        Ok(ExportPost {
            post: Post::from_row(row)?,
            image_data: row.try_get("image_data")?,
            asset_key: row.try_get("asset_key")?,
        })
    }
}

fn build_archive(
    mut receiver: mpsc::Receiver<Result<ExportPost, sqlx::Error>>,
    author: User,
) -> Result<Vec<u8>, Error> {
    let mut writer = ZipWriter::new(Cursor::new(Vec::new()));
    let mut items = Vec::new();
    while let Some(post) = receiver.blocking_recv() {
        let post = post?;
        let image_path = format!("images/{:06}.jpg", items.len() + 1);
        writer.start_file(
            image_path.as_str(),
            SimpleFileOptions::default().compression_method(CompressionMethod::Stored),
        )?;
        writer.write_all(&post.image_data)?;
        items.push(ExportPostResponse {
            create_post_response: new_post_response(&post.post, &author),
            image_path,
        });
    }
    writer.start_file("posts.json", SimpleFileOptions::default())?;
    let mut manifest = serde_json::to_vec(&items)?;
    manifest.push(b'\n');
    writer.write_all(&manifest)?;
    Ok(writer.finish()?.into_inner())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::post::PostLocation;
    use chrono::Utc;
    use std::io::Read;

    fn author() -> User {
        let mut user = User {
            id: "author".into(),
            username: "author".into(),
            display_name: "Author".into(),
            secret_key_hash: "ab".repeat(32),
            status: "active".into(),
            region: "asia".into(),
            ..Default::default()
        };
        user.set_image_key().unwrap();
        user
    }

    fn post(id: &str, image_data: Vec<u8>) -> ExportPost {
        let now = Utc::now();
        ExportPost {
            post: Post {
                id: id.into(),
                author_id: "author".into(),
                posting_window_id: "window".into(),
                image_id: format!("image-{id}"),
                caption: format!("caption-{id}"),
                visibility: "public".into(),
                post_location: PostLocation {
                    kind: "none".into(),
                    ..Default::default()
                },
                posted_at: now,
                on_time_deadline: now,
                window_ended_at: None,
            },
            image_data,
            asset_key: None,
        }
    }

    #[tokio::test]
    async fn streamed_export_preserves_images_order_and_manifest() {
        let (sender, receiver) = mpsc::channel(1);
        let archive = tokio::task::spawn_blocking(move || build_archive(receiver, author()));
        let images = [
            include_bytes!("../tests/fixtures/small.jpg").as_slice(),
            include_bytes!("../tests/fixtures/wide.jpg").as_slice(),
        ];
        sender
            .send(Ok(post("first", images[0].to_vec())))
            .await
            .unwrap();
        sender
            .send(Ok(post("second", images[1].to_vec())))
            .await
            .unwrap();
        drop(sender);
        let data = archive.await.unwrap().unwrap();
        let mut zip = zip::ZipArchive::new(Cursor::new(data)).unwrap();
        assert_eq!(zip.len(), 3);
        for (index, expected) in images.into_iter().enumerate() {
            let name = format!("images/{:06}.jpg", index + 1);
            let mut file = zip.by_index(index).unwrap();
            assert_eq!(file.name().unwrap(), name);
            assert_eq!(file.compression(), CompressionMethod::Stored);
            let mut bytes = Vec::new();
            file.read_to_end(&mut bytes).unwrap();
            assert_eq!(bytes, expected);
        }
        let mut manifest = String::new();
        zip.by_name("posts.json")
            .unwrap()
            .read_to_string(&mut manifest)
            .unwrap();
        assert!(manifest.ends_with('\n'));
        let items: serde_json::Value = serde_json::from_str(&manifest).unwrap();
        assert_eq!(items.as_array().unwrap().len(), 2);
        assert_eq!(items[0]["id"], "first");
        assert_eq!(items[1]["id"], "second");
        assert_eq!(items[0]["imagePath"], "images/000001.jpg");
        assert_eq!(items[1]["imagePath"], "images/000002.jpg");
        assert_eq!(items[0]["caption"], "caption-first");
        assert_eq!(items[0]["onTime"], true);
        assert_eq!(items[0]["author"]["id"], "author");
    }

    #[tokio::test]
    async fn export_rejects_partial_database_results_and_supports_empty_history() {
        let (sender, receiver) = mpsc::channel(1);
        let archive = tokio::task::spawn_blocking(move || build_archive(receiver, author()));
        sender.send(Ok(post("first", vec![1, 2, 3]))).await.unwrap();
        sender.send(Err(sqlx::Error::RowNotFound)).await.unwrap();
        drop(sender);
        assert!(archive.await.unwrap().is_err());
        let (sender, receiver) = mpsc::channel(1);
        drop(sender);
        let data = tokio::task::spawn_blocking(move || build_archive(receiver, author()))
            .await
            .unwrap()
            .unwrap();
        let mut zip = zip::ZipArchive::new(Cursor::new(data)).unwrap();
        assert_eq!(zip.len(), 1);
        let mut manifest = String::new();
        zip.by_name("posts.json")
            .unwrap()
            .read_to_string(&mut manifest)
            .unwrap();
        assert_eq!(manifest, "[]\n");
    }
    #[tokio::test]
    async fn partition_hint_matches_full_export_and_falls_back_for_moved_images() {
        use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
        use std::str::FromStr;
        let Ok(dsn) = std::env::var("ISUREAL_SESSION_TEST_DSN") else {
            return;
        };
        let schema = format!("astra_export_{}", uuid::Uuid::new_v4().simple());
        let admin = sqlx::PgPool::connect(&dsn).await.unwrap();
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .unwrap();
        let db = PgPoolOptions::new()
            .max_connections(2)
            .connect_with(
                PgConnectOptions::from_str(&dsn)
                    .unwrap()
                    .options([("search_path", schema.as_str())]),
            )
            .await
            .unwrap();
        sqlx::raw_sql(r#"
          CREATE TABLE posting_windows (id text PRIMARY KEY, on_time_deadline timestamptz, ended_at timestamptz);
          CREATE TABLE posts (id text PRIMARY KEY, author_id text, posting_window_id text,
            image_id text, caption text, visibility text, location_kind text,
            latitude numeric, longitude numeric, locality text, posted_at timestamptz, deleted_at timestamptz);
          CREATE TABLE image_objects (id text, data bytea, asset_key text, created_at timestamptz,
            PRIMARY KEY(id,created_at)) PARTITION BY RANGE(created_at);
          CREATE TABLE image_objects_old PARTITION OF image_objects FOR VALUES FROM ('2020-01-01') TO ('2021-01-01');
          CREATE TABLE image_objects_new PARTITION OF image_objects FOR VALUES FROM ('2021-01-01') TO ('2022-01-01');
          CREATE TABLE image_objects_default PARTITION OF image_objects DEFAULT;
          INSERT INTO posting_windows VALUES ('window','2020-08-01','2022-01-01');
          INSERT INTO posts
          SELECT 'p-'||i, CASE WHEN i<=20 THEN 'matched' ELSE 'mixed' END, 'window', 'image-'||i,
            'caption-'||i, CASE WHEN i%2=0 THEN 'friends' ELSE 'public' END,
            CASE WHEN i%3=0 THEN 'exact' ELSE 'none' END,
            CASE WHEN i%3=0 THEN 35.6 ELSE NULL END, CASE WHEN i%3=0 THEN 139.5 ELSE NULL END,
            NULL, '2020-01-01'::timestamptz+(i/2)*interval '1 day',
            CASE WHEN i%17=0 THEN '2021-01-01'::timestamptz ELSE NULL END
          FROM generate_series(1,100) i;
          INSERT INTO image_objects (id,data,created_at)
          SELECT image_id, convert_to('bytes-'||id,'UTF8'),
            CASE WHEN author_id='matched' THEN posted_at ELSE posted_at+interval '1 year' END
          FROM posts WHERE id<>'p-99';
          CREATE INDEX ON posts(author_id,posted_at,id);
          ANALYZE;
        "#).execute(&db).await.unwrap();
        let baseline = format!(
            "SELECT {POST_COLUMNS}, i.data AS image_data, i.asset_key FROM posts p \
            JOIN posting_windows w ON w.id=p.posting_window_id \
            JOIN image_objects i ON i.id=p.image_id \
            WHERE p.author_id=$1 AND p.deleted_at IS NULL ORDER BY p.posted_at,p.id"
        );
        for actor in ["matched", "mixed", "absent"] {
            let old: Vec<ExportPost> = sqlx::query_as(&baseline)
                .bind(actor)
                .fetch_all(&db)
                .await
                .unwrap();
            let new: Vec<ExportPost> = sqlx::query_as(&export_query())
                .bind(actor)
                .fetch_all(&db)
                .await
                .unwrap();
            assert_eq!(format!("{new:?}"), format!("{old:?}"), "{actor}");
        }
        // Deletion/recreation under the same ID and movement between partitions
        // must not make a post disappear or retain its old image bytes.
        sqlx::raw_sql("UPDATE image_objects SET created_at='2025-02-03', data='changed'::bytea WHERE id='image-1'; \
            DELETE FROM image_objects WHERE id='image-2'; \
            INSERT INTO image_objects (id,data,created_at) VALUES ('image-2','recreated'::bytea,'2021-05-01')")
            .execute(&db).await.unwrap();
        let old: Vec<ExportPost> = sqlx::query_as(&baseline)
            .bind("matched")
            .fetch_all(&db)
            .await
            .unwrap();
        let new: Vec<ExportPost> = sqlx::query_as(&export_query())
            .bind("matched")
            .fetch_all(&db)
            .await
            .unwrap();
        assert_eq!(format!("{new:?}"), format!("{old:?}"));
        db.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await
            .unwrap();
        admin.close().await;
    }
}
