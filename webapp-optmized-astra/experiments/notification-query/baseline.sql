WITH active_windows AS MATERIALIZED (
    SELECT w.id,w.region,
           EXISTS(SELECT 1 FROM astra_notification_batches b WHERE b.posting_window_id=w.id) AS batched
    FROM posting_windows w WHERE w.ended_at IS NULL
), recipients AS MATERIALIZED (
    SELECT s.slot,s.connected_at,u.id AS user_id,u.secret_key_hash
    FROM unnest($1::text[], $2::text[], $3::timestamptz[])
         WITH ORDINALITY AS s(user_id,region,connected_at,slot)
    LEFT JOIN users u ON u.id=s.user_id AND u.status='active' AND u.region=s.region
), legacy AS (
    SELECT s.slot,n.id,n.posting_window_id,n.region,n.created_at
    FROM recipients s
    CROSS JOIN LATERAL (
        SELECT n.id,n.posting_window_id,w.region,n.created_at
        FROM active_windows w
        CROSS JOIN LATERAL (
            SELECT n.id,n.posting_window_id,n.created_at
            FROM notifications n
            WHERE NOT w.batched AND n.posting_window_id=w.id AND n.user_id=s.user_id
              AND n.created_at>s.connected_at
            ORDER BY n.created_at,n.id LIMIT 1
        ) n
        ORDER BY n.created_at,n.id LIMIT 1
    ) n
), cached AS (
    SELECT c.slot,c.id,c.posting_window_id,w.region,c.created_at
    FROM unnest($4::bigint[], $5::text[], $6::text[], $7::timestamptz[])
         AS c(slot,id,posting_window_id,created_at)
    JOIN recipients s ON s.slot=c.slot AND s.user_id IS NOT NULL
    JOIN active_windows w ON w.id=c.posting_window_id AND w.batched
    WHERE c.created_at>s.connected_at
), first_notice AS (
    SELECT DISTINCT ON (slot) slot,id,posting_window_id,region,created_at
    FROM (SELECT * FROM legacy UNION ALL SELECT * FROM cached) n
    ORDER BY slot,created_at,id
)
SELECT s.slot,s.secret_key_hash,n.id,n.posting_window_id,n.region,n.created_at
FROM recipients s LEFT JOIN first_notice n ON n.slot=s.slot
