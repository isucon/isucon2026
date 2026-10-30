-- Append one invalidation per owner and transaction. Independent writers never
-- contend on a shared revision row. Count committed entries rather than MAX(xid):
-- transactions may commit in a different order from their IDs.
CREATE TABLE IF NOT EXISTS astra_history_epoch (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  epoch text NOT NULL DEFAULT gen_random_uuid()::text
);
INSERT INTO astra_history_epoch(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS astra_history_changes (
  owner_id text NOT NULL,
  transaction_id bigint NOT NULL,
  PRIMARY KEY(owner_id, transaction_id)
);
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
    GRANT SELECT, UPDATE ON astra_history_epoch TO isureal;
    GRANT SELECT, INSERT, TRUNCATE ON astra_history_changes TO isureal;
  END IF;
END $$;

CREATE OR REPLACE FUNCTION astra_history_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_ids text[]; post_ids text[];
BEGIN
  IF TG_LEVEL = 'STATEMENT' THEN
    owner_ids := ARRAY['']; -- window changes and explicit TRUNCATE invalidate all owners
  ELSIF TG_TABLE_NAME = 'posts' THEN
    IF TG_OP <> 'INSERT' THEN owner_ids := array_append(owner_ids, OLD.author_id); END IF;
    IF TG_OP <> 'DELETE' THEN owner_ids := array_append(owner_ids, NEW.author_id); END IF;
  ELSIF TG_TABLE_NAME IN ('comments', 'realmojis') THEN
    IF TG_OP <> 'INSERT' THEN post_ids := array_append(post_ids, OLD.post_id); END IF;
    IF TG_OP <> 'DELETE' THEN post_ids := array_append(post_ids, NEW.post_id); END IF;
    SELECT array_agg(DISTINCT author_id) INTO owner_ids FROM posts WHERE id = ANY(post_ids);
  ELSE -- image_objects and its partitions
    IF TG_OP <> 'INSERT' THEN owner_ids := array_append(owner_ids, OLD.owner_user_id); END IF;
    IF TG_OP <> 'DELETE' THEN owner_ids := array_append(owner_ids, NEW.owner_user_id); END IF;
  END IF;
  INSERT INTO astra_history_changes(owner_id, transaction_id)
    SELECT DISTINCT id, txid_current() FROM unnest(owner_ids) id WHERE id IS NOT NULL
    ON CONFLICT DO NOTHING;
  RETURN NULL;
END $$;

DO $$ DECLARE name text; BEGIN
  FOREACH name IN ARRAY ARRAY['posts','comments','realmojis','image_objects'] LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS astra_history_change ON %I', name);
    EXECUTE format('CREATE TRIGGER astra_history_change AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION astra_history_changed()', name);
    EXECUTE format('DROP TRIGGER IF EXISTS astra_history_truncate ON %I', name);
    EXECUTE format('CREATE TRIGGER astra_history_truncate AFTER TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION astra_history_changed()', name);
  END LOOP;
END $$;
DROP TRIGGER IF EXISTS astra_history_change ON posting_windows;
CREATE TRIGGER astra_history_change AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON posting_windows
  FOR EACH STATEMENT EXECUTE FUNCTION astra_history_changed();
