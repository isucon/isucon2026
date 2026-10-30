use std::io::{self, Write};
use std::sync::{Arc, LazyLock};

use axum::Extension;
use axum::body::Body;
use axum::extract::State;
use axum::http::{HeaderValue, StatusCode, header};
use axum::response::Response;
use bytes::Bytes;
use serde::Serialize;
use sqlx::postgres::PgRow;
use sqlx::{FromRow, Row};
use tokio::sync::{Semaphore, mpsc};
use zip::write::SimpleFileOptions;
use zip::{CompressionMethod, ZipWriter};

use crate::api_error::Error;
use crate::post::{POST_COLUMNS, Post};
use crate::post_api::{CreatePostResponse, new_post_response};
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
    let cached = crate::image_api::cached_export_images(user_id);
    let query = export_query();
    let mut rows = sqlx::query_as::<_, ExportPost>(&query)
        .bind(user_id)
        .bind(&cached.ids)
        .bind(&cached.versions)
        .bind(&cached.timestamps)
        .fetch_all(&h.db)
        .await?;
    let mut missing = Vec::new();
    let mut keys = Vec::new();
    for (index, row) in rows.iter_mut().enumerate() {
        if row.cache_hit {
            row.image_data = cached
                .bytes
                .get(&row.post.image_id)
                .ok_or_else(|| sqlx::Error::Protocol("missing pinned export image".into()))?
                .clone();
        } else if let Some(key) = &row.asset_key {
            missing.push(index);
            keys.push(key.clone());
        }
    }
    if !keys.is_empty() {
        let store = h
            .image_store
            .as_ref()
            .ok_or_else(|| sqlx::Error::Protocol("image store unavailable".into()))?;
        let images = store.get_many(&keys).await?;
        for (index, bytes) in missing.into_iter().zip(images) {
            rows[index].image_data = bytes;
        }
    }
    // Finish SQL/immutable-file reads before sending a successful response.
    // Keep only the image buffers; do not allocate another complete ZIP.
    let (sender, receiver) = mpsc::channel(2);
    tokio::task::spawn_blocking(move || {
        let _permit = permit;
        let output = ZipChunks::new(sender.clone());
        if let Err(err) = write_archive(rows, author, output) {
            let _ = sender.blocking_send(Err(io::Error::other(err.to_string())));
        }
    });
    let stream = futures_util::stream::unfold(receiver, |mut receiver| async {
        receiver.recv().await.map(|chunk| (chunk, receiver))
    });
    let mut response = Response::new(Body::from_stream(stream));
    *response.status_mut() = StatusCode::OK;
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/zip"),
    );
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
   WITH cached AS MATERIALIZED (
     SELECT * FROM unnest($2::text[],$3::text[],$4::timestamptz[]) AS c(id,version,created_at)
   )
   SELECT {POST_COLUMNS},
     CASE WHEN (i.created_at=c.created_at AND i.version=c.version)
       OR (i.asset_key IS NOT NULL AND 'asset:'||i.asset_key=c.version)
       THEN NULL ELSE i.data END AS image_data, i.asset_key
   FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   JOIN LATERAL (
     WITH hinted AS MATERIALIZED (
       SELECT id, data, asset_key, created_at, xmin::text AS version FROM image_objects
       WHERE id = p.image_id AND created_at = p.posted_at
     )
     SELECT id, data, asset_key, created_at, version FROM hinted
     UNION ALL
     SELECT id, data, asset_key, created_at, xmin::text AS version FROM image_objects
     WHERE id = p.image_id AND NOT EXISTS (SELECT 1 FROM hinted)
   ) i ON true
   LEFT JOIN cached c ON c.id=i.id
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
   ORDER BY p.posted_at, p.id
  "#
    )
}

static EXPORTS: LazyLock<Semaphore> = LazyLock::new(|| {
    let concurrency = std::env::var("ISUREAL_EXPORT_CONCURRENCY")
        .unwrap_or_else(|_| "2".into())
        .parse::<usize>()
        .expect("export concurrency");
    assert!((1..=16).contains(&concurrency), "invalid export concurrency");
    Semaphore::new(concurrency)
});

#[derive(Debug)]
struct ExportPost {
    post: Post,
    image_data: Bytes,
    asset_key: Option<String>,
    cache_hit: bool,
}
impl FromRow<'_, PgRow> for ExportPost {
    fn from_row(row: &PgRow) -> sqlx::Result<ExportPost> {
        let data: Option<Vec<u8>> = row.try_get("image_data")?;
        Ok(ExportPost {
            cache_hit: data.is_none(),
            post: Post::from_row(row)?,
            image_data: data.unwrap_or_default().into(),
            asset_key: row.try_get("asset_key")?,
        })
    }
}

const ZIP_CHUNK_BYTES: usize = 64 * 1024;

/// Bounded chunks propagate HTTP backpressure to the blocking ZIP encoder.
struct ZipChunks {
    sender: mpsc::Sender<io::Result<Bytes>>,
    buffer: Vec<u8>,
}

impl ZipChunks {
    fn new(sender: mpsc::Sender<io::Result<Bytes>>) -> Self {
        Self {
            sender,
            buffer: Vec::with_capacity(ZIP_CHUNK_BYTES),
        }
    }
}

impl Write for ZipChunks {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        let count = bytes.len().min(ZIP_CHUNK_BYTES - self.buffer.len());
        self.buffer.extend_from_slice(&bytes[..count]);
        if self.buffer.len() == ZIP_CHUNK_BYTES {
            self.flush()?;
        }
        Ok(count)
    }

    fn flush(&mut self) -> io::Result<()> {
        if !self.buffer.is_empty() {
            let buffer = std::mem::replace(&mut self.buffer, Vec::with_capacity(ZIP_CHUNK_BYTES));
            self.sender
                .blocking_send(Ok(Bytes::from(buffer)))
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::BrokenPipe, "export client disconnected")
                })?;
        }
        Ok(())
    }
}

fn write_archive<W: Write>(posts: Vec<ExportPost>, author: User, output: W) -> Result<(), Error> {
    let mut writer = ZipWriter::new_stream(output);
    let mut items = Vec::new();
    for post in posts {
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
    writer.finish()?.flush()?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::post::PostLocation;
    use chrono::Utc;
    use std::io::{Cursor, Read};

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
            image_data: image_data.into(),
            asset_key: None,
            cache_hit: false,
        }
    }

    #[tokio::test]
    async fn streamed_export_preserves_images_order_and_manifest() {
        let images = [
            include_bytes!("../tests/fixtures/small.jpg").as_slice(),
            include_bytes!("../tests/fixtures/wide.jpg").as_slice(),
        ];
        let posts = vec![
            post("first", images[0].to_vec()),
            post("second", images[1].to_vec()),
        ];
        let (sender, mut receiver) = mpsc::channel(2);
        let archive = tokio::task::spawn_blocking(move || {
            write_archive(posts, author(), ZipChunks::new(sender))
        });
        let mut data = Vec::new();
        while let Some(chunk) = receiver.recv().await {
            data.extend_from_slice(&chunk.unwrap());
        }
        archive.await.unwrap().unwrap();
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
    async fn slow_or_disconnected_export_bounds_output_and_releases_writer() {
        let (sender, mut receiver) = mpsc::channel(2);
        let mut worker = tokio::task::spawn_blocking(move || {
            write_archive(
                vec![post("large", vec![7; 8 * ZIP_CHUNK_BYTES])],
                author(),
                ZipChunks::new(sender),
            )
        });
        let first = receiver.recv().await.unwrap().unwrap();
        assert_eq!(first.len(), ZIP_CHUNK_BYTES);
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(30), &mut worker)
                .await
                .is_err()
        );
        assert!(receiver.len() <= 2);
        drop(receiver);
        assert!(
            tokio::time::timeout(std::time::Duration::from_secs(2), worker)
                .await
                .unwrap()
                .unwrap()
                .is_err()
        );
    }

    #[tokio::test]
    async fn export_supports_empty_history() {
        let mut data = Vec::new();
        write_archive(Vec::new(), author(), &mut data).unwrap();
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
                .bind(Vec::<String>::new())
                .bind(Vec::<String>::new())
                .bind(Vec::<chrono::DateTime<Utc>>::new())
                .fetch_all(&db)
                .await
                .unwrap();
            assert_eq!(format!("{new:?}"), format!("{old:?}"), "{actor}");
        }
        let memo: Vec<(String,String,chrono::DateTime<Utc>)> = sqlx::query_as("SELECT id,xmin::text,created_at FROM image_objects WHERE id IN ('image-1','image-2','image-3') ORDER BY id").fetch_all(&db).await.unwrap();
        let ids: Vec<_> = memo.iter().map(|r| r.0.clone()).collect();
        let versions: Vec<_> = memo.iter().map(|r| r.1.clone()).collect();
        let timestamps: Vec<_> = memo.iter().map(|r| r.2).collect();
        let cached: Vec<ExportPost> = sqlx::query_as(&export_query())
            .bind("matched")
            .bind(&ids)
            .bind(&versions)
            .bind(&timestamps)
            .fetch_all(&db)
            .await
            .unwrap();
        assert_eq!(cached.iter().filter(|r| r.cache_hit).count(), 3);
        assert!(
            cached
                .iter()
                .filter(|r| r.cache_hit)
                .all(|r| r.image_data.is_empty())
        );
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
            .bind(Vec::<String>::new())
            .bind(Vec::<String>::new())
            .bind(Vec::<chrono::DateTime<Utc>>::new())
            .fetch_all(&db)
            .await
            .unwrap();
        assert_eq!(format!("{new:?}"), format!("{old:?}"));
        let cached: Vec<ExportPost> = sqlx::query_as(&export_query())
            .bind("matched")
            .bind(&ids)
            .bind(&versions)
            .bind(&timestamps)
            .fetch_all(&db)
            .await
            .unwrap();
        let hits: Vec<_> = cached
            .iter()
            .filter(|r| r.cache_hit)
            .map(|r| r.post.image_id.as_str())
            .collect();
        assert_eq!(hits, ["image-3"]);
        assert_eq!(
            cached
                .iter()
                .find(|r| r.post.image_id == "image-1")
                .unwrap()
                .image_data
                .as_ref(),
            b"changed"
        );
        assert_eq!(
            cached
                .iter()
                .find(|r| r.post.image_id == "image-2")
                .unwrap()
                .image_data
                .as_ref(),
            b"recreated"
        );
        sqlx::query("UPDATE posts SET deleted_at=now() WHERE image_id='image-3'")
            .execute(&db)
            .await
            .unwrap();
        let deleted: Vec<ExportPost> = sqlx::query_as(&export_query())
            .bind("matched")
            .bind(&ids)
            .bind(&versions)
            .bind(&timestamps)
            .fetch_all(&db)
            .await
            .unwrap();
        assert!(!deleted.iter().any(|r| r.post.image_id == "image-3"));
        db.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await
            .unwrap();
        admin.close().await;
    }
}
