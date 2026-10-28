BEGIN;

-- Freeze notification eligibility per region, without blocking profile edits
-- or registration in other regions while a posting window is being created.
CREATE OR REPLACE FUNCTION astra_lock_notification_eligibility()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_region integer;
    new_region integer;
    region_id integer;
BEGIN
    IF TG_OP = 'UPDATE' AND (OLD.region, OLD.status) IS NOT DISTINCT FROM (NEW.region, NEW.status) THEN
        RETURN NEW;
    END IF;
    IF TG_OP <> 'INSERT' THEN
        old_region := CASE OLD.region WHEN 'asia' THEN 0 WHEN 'europe' THEN 1 WHEN 'america' THEN 2 END;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_region := CASE NEW.region WHEN 'asia' THEN 0 WHEN 'europe' THEN 1 WHEN 'america' THEN 2 END;
    END IF;
    -- Region changes acquire both shared locks in the same order.
    FOR region_id IN SELECT DISTINCT id FROM unnest(ARRAY[old_region,new_region]) AS regions(id)
                     WHERE id IS NOT NULL ORDER BY id LOOP
        PERFORM pg_advisory_xact_lock_shared(1095980114, region_id);
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS astra_notification_eligibility ON users;
CREATE TRIGGER astra_notification_eligibility
BEFORE INSERT OR DELETE OR UPDATE OF region, status ON users
FOR EACH ROW EXECUTE FUNCTION astra_lock_notification_eligibility();

COMMIT;
