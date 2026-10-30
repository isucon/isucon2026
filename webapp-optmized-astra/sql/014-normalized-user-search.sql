-- Derived text only. Raw names are checked on every search so old binaries,
-- imports and initialization cannot make a stale index hide a matching user.
CREATE TABLE IF NOT EXISTS astra_user_search (
    user_id text PRIMARY KEY,
    source_username text NOT NULL,
    source_display_name text NOT NULL,
    normalized_username text COLLATE "C" NOT NULL,
    normalized_display_name text COLLATE "C" NOT NULL
);
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON astra_user_search TO isureal;
    END IF;
END $$;
