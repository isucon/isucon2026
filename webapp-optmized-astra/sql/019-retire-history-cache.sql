-- Measurements did not show a repeatable score gain from the history cache.
-- Fresh batched reads no longer require a write-side invalidation ledger.
DO $$ DECLARE name text; BEGIN
  FOREACH name IN ARRAY ARRAY['posts','comments','realmojis','image_objects','posting_windows'] LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS astra_history_change ON %I', name);
    EXECUTE format('DROP TRIGGER IF EXISTS astra_history_truncate ON %I', name);
  END LOOP;
END $$;
DROP FUNCTION IF EXISTS astra_history_changed();
DROP TABLE IF EXISTS astra_history_changes, astra_history_epoch;
