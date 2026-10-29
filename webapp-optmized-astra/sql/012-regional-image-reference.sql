-- Region-qualified immutable asset references; legacy UUIDs remain in Asia.
ALTER TABLE image_objects ALTER COLUMN asset_key TYPE varchar(64);
