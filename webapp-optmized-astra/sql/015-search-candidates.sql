-- Candidate text only. Account state, keys and block relations remain fresh
-- SQL reads. Track missing/stale normalization so matching candidates can use
-- a text index without joining every user before applying the search pattern.
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
CREATE TABLE IF NOT EXISTS astra_search_dirty (user_id text PRIMARY KEY);
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON astra_search_dirty TO isureal;
    END IF;
END $$;

CREATE OR REPLACE FUNCTION astra_track_search_user() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        DELETE FROM astra_user_search WHERE user_id=OLD.id;
        DELETE FROM astra_search_dirty WHERE user_id=OLD.id;
        RETURN OLD;
    END IF;
    IF TG_OP='UPDATE' AND NEW.id IS DISTINCT FROM OLD.id THEN
        DELETE FROM astra_user_search WHERE user_id=OLD.id;
        DELETE FROM astra_search_dirty WHERE user_id=OLD.id;
    END IF;
    IF TG_OP='INSERT' OR NEW.id IS DISTINCT FROM OLD.id OR NEW.username IS DISTINCT FROM OLD.username
       OR NEW.display_name IS DISTINCT FROM OLD.display_name THEN
        INSERT INTO astra_search_dirty VALUES (NEW.id) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS astra_track_search_user ON users;
CREATE TRIGGER astra_track_search_user AFTER INSERT OR UPDATE OF id,username,display_name OR DELETE ON users
FOR EACH ROW EXECUTE FUNCTION astra_track_search_user();

CREATE OR REPLACE FUNCTION astra_track_search_index() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='TRUNCATE' THEN
        INSERT INTO astra_search_dirty SELECT id FROM users ON CONFLICT DO NOTHING;
        RETURN NULL;
    ELSIF TG_OP='DELETE' THEN
        INSERT INTO astra_search_dirty VALUES (OLD.user_id) ON CONFLICT DO NOTHING;
        RETURN OLD;
    END IF;
    IF TG_OP='UPDATE' AND NEW.user_id IS DISTINCT FROM OLD.user_id THEN
        INSERT INTO astra_search_dirty VALUES (OLD.user_id) ON CONFLICT DO NOTHING;
    END IF;
    IF EXISTS (SELECT 1 FROM users WHERE id=NEW.user_id
        AND username=NEW.source_username AND display_name=NEW.source_display_name) THEN
        DELETE FROM astra_search_dirty WHERE user_id=NEW.user_id;
    ELSE
        INSERT INTO astra_search_dirty VALUES (NEW.user_id) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS astra_track_search_index_row ON astra_user_search;
CREATE TRIGGER astra_track_search_index_row AFTER INSERT OR UPDATE OR DELETE ON astra_user_search
FOR EACH ROW EXECUTE FUNCTION astra_track_search_index();
DROP TRIGGER IF EXISTS astra_track_search_index_truncate ON astra_user_search;
CREATE TRIGGER astra_track_search_index_truncate AFTER TRUNCATE ON astra_user_search
FOR EACH STATEMENT EXECUTE FUNCTION astra_track_search_index();

INSERT INTO astra_search_dirty
SELECT u.id FROM users u LEFT JOIN astra_user_search s ON s.user_id=u.id
WHERE s.user_id IS NULL OR s.source_username IS DISTINCT FROM u.username
    OR s.source_display_name IS DISTINCT FROM u.display_name
ON CONFLICT DO NOTHING;
CREATE INDEX IF NOT EXISTS astra_search_username_trgm ON astra_user_search USING gin (normalized_username public.gin_trgm_ops) WITH (fastupdate=off);
CREATE INDEX IF NOT EXISTS astra_search_display_trgm ON astra_user_search USING gin (normalized_display_name public.gin_trgm_ops) WITH (fastupdate=off);
