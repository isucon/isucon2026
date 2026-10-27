ALTER TABLE image_objects ALTER COLUMN data SET STORAGE EXTERNAL;
DROP INDEX IF EXISTS posts_posting_window_id;
DROP INDEX IF EXISTS posts_created_at;
DROP INDEX IF EXISTS comments_created_at;
DROP INDEX IF EXISTS realmojis_created_at;
DROP INDEX IF EXISTS image_objects_created_at;
