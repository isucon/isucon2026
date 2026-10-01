DROP INDEX IF EXISTS index_contestants_on_discord_id;
CREATE UNIQUE INDEX index_contestants_on_discord_id ON contestants(discord_id) WHERE discord_id <> '';

CREATE TABLE IF NOT EXISTS registration_records (
  -- Keep consent evidence even when registration withdrawal deletes the contestant.
  contestant_id bigint PRIMARY KEY,
  github_id text NOT NULL,
  terms_agreed_at timestamptz,
  privacy_agreed_at timestamptz,
  sponsor_provide_agreed_at timestamptz,
  sponsor_provide_withdrawn_at timestamptz,
  minor_parental_consent boolean,
  email text NOT NULL DEFAULT '',
  tshirt_size text NOT NULL DEFAULT ''
);

-- Source-only attributes retain their original values and are visible to staff.
CREATE TABLE IF NOT EXISTS registration_source_records (
  model text NOT NULL,
  source_id bigint NOT NULL,
  payload jsonb NOT NULL,
  PRIMARY KEY(model, source_id)
);
