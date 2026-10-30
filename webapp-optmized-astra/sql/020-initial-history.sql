-- Immutable startup snapshots use fresh users and this conservative dirty set.
-- A dirty owner/day stays dirty across resets and deployments. No revision
-- counter is inferred from transaction IDs or commit order.
CREATE TABLE IF NOT EXISTS astra_initial_history_epoch (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 epoch text NOT NULL DEFAULT gen_random_uuid()::text
);
INSERT INTO astra_initial_history_epoch(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS astra_initial_history_dirty (
 owner_id text NOT NULL, local_date date NOT NULL,
 PRIMARY KEY(owner_id,local_date)
);
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
  GRANT SELECT,UPDATE ON astra_initial_history_epoch TO isureal;
  GRANT SELECT,INSERT ON astra_initial_history_dirty TO isureal;
 END IF;
END $$;
CREATE OR REPLACE FUNCTION astra_initial_history_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ids text[];
BEGIN
 IF TG_LEVEL='STATEMENT' THEN
  UPDATE astra_initial_history_epoch SET epoch=gen_random_uuid()::text;
 ELSIF TG_TABLE_NAME='posting_windows' THEN
  IF TG_OP='INSERT' THEN RETURN NULL; END IF;
  IF TG_OP='UPDATE' AND (OLD.id,OLD.local_date,OLD.on_time_deadline)
   IS NOT DISTINCT FROM (NEW.id,NEW.local_date,NEW.on_time_deadline) THEN RETURN NULL; END IF;
  UPDATE astra_initial_history_epoch SET epoch=gen_random_uuid()::text;
 ELSIF TG_TABLE_NAME='posts' THEN
  IF TG_OP<>'INSERT' THEN
   INSERT INTO astra_initial_history_dirty SELECT OLD.author_id,local_date FROM posting_windows WHERE id=OLD.posting_window_id ON CONFLICT DO NOTHING;
  END IF;
  IF TG_OP<>'DELETE' THEN
   INSERT INTO astra_initial_history_dirty SELECT NEW.author_id,local_date FROM posting_windows WHERE id=NEW.posting_window_id ON CONFLICT DO NOTHING;
  END IF;
 ELSE
  IF TG_OP<>'INSERT' THEN ids:=array_append(ids,OLD.post_id); END IF;
  IF TG_OP<>'DELETE' THEN ids:=array_append(ids,NEW.post_id); END IF;
  INSERT INTO astra_initial_history_dirty
   SELECT DISTINCT p.author_id,w.local_date FROM posts p JOIN posting_windows w ON w.id=p.posting_window_id
   WHERE p.id=ANY(ids) ON CONFLICT DO NOTHING;
 END IF;
 RETURN NULL;
END $$;
DO $$ DECLARE name text; BEGIN
 FOREACH name IN ARRAY ARRAY['posts','comments','realmojis','posting_windows'] LOOP
  EXECUTE format('DROP TRIGGER IF EXISTS astra_initial_history_change ON %I',name);
  EXECUTE format('CREATE TRIGGER astra_initial_history_change AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION astra_initial_history_changed()',name);
  EXECUTE format('DROP TRIGGER IF EXISTS astra_initial_history_truncate ON %I',name);
  EXECUTE format('CREATE TRIGGER astra_initial_history_truncate AFTER TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION astra_initial_history_changed()',name);
 END LOOP;
END $$;
-- Existing post-boundary records are omitted from the snapshot and may be
-- removed by reset with triggers disabled. Their periods must always use SQL.
INSERT INTO astra_initial_history_dirty
 SELECT DISTINCT p.author_id,w.local_date FROM (
  SELECT id AS post_id FROM posts WHERE created_at>(SELECT created_at FROM initial_data_boundary)
  UNION SELECT post_id FROM comments WHERE created_at>(SELECT created_at FROM initial_data_boundary)
  UNION SELECT post_id FROM realmojis WHERE created_at>(SELECT created_at FROM initial_data_boundary)
 ) changed JOIN posts p ON p.id=changed.post_id JOIN posting_windows w ON w.id=p.posting_window_id
 ON CONFLICT DO NOTHING;
