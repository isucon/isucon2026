-- Enforce username uniqueness without serializing all user registrations.
CREATE UNIQUE INDEX IF NOT EXISTS astra_users_username_unique ON users(username);
