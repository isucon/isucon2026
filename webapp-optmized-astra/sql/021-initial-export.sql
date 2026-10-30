-- Export can merge unchanged initial metadata with fresh post-boundary posts.
-- Comments/reactions do not affect its manifest or image list.
CREATE TABLE IF NOT EXISTS astra_initial_export_dirty(owner_id text PRIMARY KEY);
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
  GRANT SELECT,INSERT ON astra_initial_export_dirty TO isureal;
 END IF;
END $$;
CREATE OR REPLACE FUNCTION astra_initial_export_post_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE boundary timestamptz;
BEGIN
 SELECT created_at INTO boundary FROM initial_data_boundary;
 IF TG_OP<>'INSERT' THEN
  IF OLD.created_at<=boundary THEN INSERT INTO astra_initial_export_dirty VALUES(OLD.author_id) ON CONFLICT DO NOTHING; END IF;
 END IF;
 IF TG_OP<>'DELETE' THEN
  IF NEW.created_at<=boundary THEN INSERT INTO astra_initial_export_dirty VALUES(NEW.author_id) ON CONFLICT DO NOTHING; END IF;
 END IF;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS astra_initial_export_post ON posts;
CREATE TRIGGER astra_initial_export_post AFTER INSERT OR UPDATE OR DELETE ON posts
 FOR EACH ROW EXECUTE FUNCTION astra_initial_export_post_changed();
CREATE OR REPLACE FUNCTION astra_initial_export_image_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ids text[];
BEGIN
 IF TG_LEVEL='STATEMENT' THEN
  UPDATE astra_initial_history_epoch SET epoch=gen_random_uuid()::text;
  RETURN NULL;
 END IF;
 IF TG_OP<>'INSERT' THEN ids:=array_append(ids,OLD.id); END IF;
 IF TG_OP<>'DELETE' THEN ids:=array_append(ids,NEW.id); END IF;
 INSERT INTO astra_initial_export_dirty
 SELECT DISTINCT author_id FROM posts WHERE image_id=ANY(ids)
 AND created_at<=(SELECT created_at FROM initial_data_boundary) ON CONFLICT DO NOTHING;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS astra_initial_export_image ON image_objects;
CREATE TRIGGER astra_initial_export_image AFTER INSERT OR UPDATE OR DELETE ON image_objects
 FOR EACH ROW EXECUTE FUNCTION astra_initial_export_image_changed();
DO $$ DECLARE name regclass; BEGIN
 FOR name IN SELECT 'image_objects'::regclass UNION SELECT inhrelid::regclass FROM pg_inherits WHERE inhparent='image_objects'::regclass LOOP
  EXECUTE format('DROP TRIGGER IF EXISTS astra_initial_export_truncate ON %s',name);
  EXECUTE format('CREATE TRIGGER astra_initial_export_truncate AFTER TRUNCATE ON %s FOR EACH STATEMENT EXECUTE FUNCTION astra_initial_export_image_changed()',name);
 END LOOP;
END $$;
