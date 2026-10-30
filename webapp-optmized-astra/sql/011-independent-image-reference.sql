-- Legacy image bytes stay in this database. A non-NULL reference is published
-- only after the corresponding immutable bytes committed in the image database.
ALTER TABLE image_objects ADD COLUMN IF NOT EXISTS asset_key varchar(64);
