//! Persist pure Unicode normalization, never account visibility or search results.
use crate::search_text::normalize;
use sqlx::{PgConnection, PgPool};

pub async fn update(
    connection: &mut PgConnection,
    id: &str,
    username: &str,
    display_name: &str,
) -> Result<(), sqlx::Error> {
    sqlx::query("INSERT INTO astra_user_search VALUES ($1,$2,$3,$4,$5) ON CONFLICT (user_id) DO UPDATE SET source_username=EXCLUDED.source_username,source_display_name=EXCLUDED.source_display_name,normalized_username=EXCLUDED.normalized_username,normalized_display_name=EXCLUDED.normalized_display_name")
        .bind(id).bind(username).bind(display_name).bind(normalize(username)).bind(normalize(display_name))
        .execute(connection).await?;
    Ok(())
}

pub async fn refresh(db: &PgPool) -> Result<(), sqlx::Error> {
    let users: Vec<(String,String,String)> = sqlx::query_as("SELECT u.id,u.username,u.display_name FROM users u LEFT JOIN astra_user_search s ON s.user_id=u.id WHERE s.user_id IS NULL OR s.source_username<>u.username OR s.source_display_name<>u.display_name ORDER BY u.id")
        .fetch_all(db).await?;
    for batch in users.chunks(512) {
        let ids: Vec<_> = batch.iter().map(|u| u.0.as_str()).collect();
        let names: Vec<_> = batch.iter().map(|u| u.1.as_str()).collect();
        let display_names: Vec<_> = batch.iter().map(|u| u.2.as_str()).collect();
        let normalized_names: Vec<_> = names.iter().map(|v| normalize(v)).collect();
        let normalized_displays: Vec<_> = display_names.iter().map(|v| normalize(v)).collect();
        sqlx::query("INSERT INTO astra_user_search SELECT x.* FROM unnest($1::text[],$2::text[],$3::text[],$4::text[],$5::text[]) AS x(id,username,display_name,normalized_username,normalized_display_name) JOIN users u ON u.id=x.id AND u.username=x.username AND u.display_name=x.display_name FOR SHARE OF u ON CONFLICT (user_id) DO UPDATE SET source_username=EXCLUDED.source_username,source_display_name=EXCLUDED.source_display_name,normalized_username=EXCLUDED.normalized_username,normalized_display_name=EXCLUDED.normalized_display_name")
            .bind(ids).bind(names).bind(display_names).bind(normalized_names).bind(normalized_displays).execute(db).await?;
    }
    Ok(())
}
