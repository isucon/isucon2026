-- An immutable initial graph plus fresh changed pairs avoids repeating large
-- graph joins. The changed pairs are read in the candidate query's snapshot.
CREATE TABLE IF NOT EXISTS astra_graph_epoch(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),epoch text NOT NULL DEFAULT gen_random_uuid()::text);
INSERT INTO astra_graph_epoch(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS astra_graph_changes(user_low_id text NOT NULL,user_high_id text NOT NULL,PRIMARY KEY(user_low_id,user_high_id));
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
  GRANT SELECT,UPDATE ON astra_graph_epoch TO isureal;
  GRANT SELECT,INSERT,TRUNCATE ON astra_graph_changes TO isureal;
 END IF;
END $$;
CREATE OR REPLACE FUNCTION astra_graph_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_LEVEL='STATEMENT' OR TG_TABLE_NAME='initial_friendships' THEN
  UPDATE astra_graph_epoch SET epoch=gen_random_uuid()::text;
  IF TG_TABLE_NAME='initial_friendships' THEN
   INSERT INTO astra_graph_changes SELECT user_low_id,user_high_id FROM initial_friendships
    UNION SELECT user_low_id,user_high_id FROM friendships ON CONFLICT DO NOTHING;
  END IF;
 ELSE
  IF TG_OP<>'INSERT' THEN INSERT INTO astra_graph_changes VALUES(OLD.user_low_id,OLD.user_high_id) ON CONFLICT DO NOTHING; END IF;
  IF TG_OP<>'DELETE' THEN INSERT INTO astra_graph_changes VALUES(NEW.user_low_id,NEW.user_high_id) ON CONFLICT DO NOTHING; END IF;
 END IF;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS astra_graph_change ON friendships;
CREATE TRIGGER astra_graph_change AFTER INSERT OR UPDATE OR DELETE ON friendships FOR EACH ROW EXECUTE FUNCTION astra_graph_changed();
DROP TRIGGER IF EXISTS astra_graph_truncate ON friendships;
CREATE TRIGGER astra_graph_truncate AFTER TRUNCATE ON friendships FOR EACH STATEMENT EXECUTE FUNCTION astra_graph_changed();
DROP TRIGGER IF EXISTS astra_graph_initial_change ON initial_friendships;
CREATE TRIGGER astra_graph_initial_change AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON initial_friendships FOR EACH STATEMENT EXECUTE FUNCTION astra_graph_changed();
INSERT INTO astra_graph_changes
 SELECT coalesce(i.user_low_id,f.user_low_id),coalesce(i.user_high_id,f.user_high_id)
 FROM initial_friendships i FULL JOIN friendships f USING(user_low_id,user_high_id)
 WHERE i.user_low_id IS NULL OR f.user_low_id IS NULL ON CONFLICT DO NOTHING;
