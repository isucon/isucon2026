-- Empty after every successful transaction. Only its commit record is used to
-- wait for replica visibility after application transactions released locks.
CREATE TABLE IF NOT EXISTS astra_visibility_barriers (id text PRIMARY KEY);
GRANT SELECT, INSERT, DELETE ON astra_visibility_barriers TO isureal;
