-- Session authority stays fixed when the user's visible region changes.
-- Asia-authoritative sessions are always authenticated against the primary.
ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_home_region text;
UPDATE users u SET auth_home_region=coalesce((SELECT i.region FROM initial_users i WHERE i.id=u.id),u.region)
 WHERE auth_home_region IS NULL;
ALTER TABLE users ALTER COLUMN auth_home_region SET NOT NULL;
CREATE OR REPLACE FUNCTION astra_session_authority() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN NEW.auth_home_region:=NEW.region;
 ELSE NEW.auth_home_region:=OLD.auth_home_region;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS astra_session_authority ON users;
CREATE TRIGGER astra_session_authority BEFORE INSERT OR UPDATE OF auth_home_region,region ON users
 FOR EACH ROW EXECUTE FUNCTION astra_session_authority();
