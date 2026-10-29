-- JPEG data is already compressed. Profiling found pglz_compress consuming
-- 26.55% of primary user CPU while sampled image values gained no compression.
-- Keep all image bytes in PostgreSQL, but skip TOAST compression attempts on
-- future inserts/updates. This recurses to partitions without rewriting rows.
ALTER TABLE image_objects ALTER COLUMN data SET STORAGE EXTERNAL;
