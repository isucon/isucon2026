-- One statement and one transaction for the post mutation, preserving locks.
CREATE OR REPLACE FUNCTION astra_create_post(
  actor text, new_post_id text, new_image_id text, image_data bytea,
  caption_value text, visibility_value text, location_kind_value text,
  latitude_value numeric, longitude_value numeric, locality_value text
) RETURNS TABLE(post_row posts, window_row posting_windows, author users)
LANGUAGE plpgsql AS $$
DECLARE
  u users; w posting_windows; p posts;
  active_count bigint; deleted_count bigint; at_time timestamptz;
BEGIN
  SELECT * INTO u FROM users WHERE id=actor FOR NO KEY UPDATE;
  IF NOT FOUND OR u.status <> 'active' THEN RAISE EXCEPTION 'user not active'; END IF;
  IF u.secret_key_hash !~ '^[0-9a-fA-F]{64}$' THEN RAISE EXCEPTION 'invalid image signing key'; END IF;
  SELECT * INTO w FROM posting_windows WHERE region=u.region AND ended_at IS NULL FOR SHARE;
  IF NOT FOUND THEN
    SELECT * INTO w FROM posting_windows WHERE region=u.region AND ended_at IS NULL FOR SHARE;
  END IF;
  IF NOT FOUND THEN
    RAISE EXCEPTION USING ERRCODE='PZ001', MESSAGE='post_slot_unavailable';
  END IF;
  SELECT count(*) FILTER(WHERE old.deleted_at IS NULL), count(*) FILTER(WHERE old.deleted_at IS NOT NULL)
    INTO active_count, deleted_count FROM posts old JOIN posting_windows pw ON pw.id=old.posting_window_id
    WHERE old.author_id=actor AND pw.local_date=w.local_date;
  at_time := clock_timestamp();
  IF active_count <> 0 OR deleted_count >= 2 OR at_time < w.starts_at THEN
    RAISE EXCEPTION USING ERRCODE='PZ001', MESSAGE='post_slot_unavailable';
  END IF;
  INSERT INTO image_objects(id,owner_user_id,data,media_type,created_at)
    VALUES(new_image_id,actor,image_data,'image/jpeg',at_time);
  INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,
    location_kind,latitude,longitude,locality,posted_at)
    VALUES(new_post_id,actor,w.id,new_image_id,caption_value,visibility_value,
      location_kind_value,latitude_value,longitude_value,locality_value,at_time)
    RETURNING * INTO p;
  RETURN QUERY SELECT p,w,u;
END $$;
