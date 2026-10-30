-- Polling probes (window, user) for each currently open window. Keep a small,
-- deduplicating user-only index for foreign-key checks when reset deletes users.
-- All historical rows and all uniqueness/foreign-key constraints remain intact.
CREATE INDEX IF NOT EXISTS astra_notifications_user ON notifications(user_id);
DROP INDEX IF EXISTS astra_notifications_user_page;
