-- One immutable recipient snapshot is published with its posting window.
-- Readers retain support for the original notifications table.
CREATE TABLE IF NOT EXISTS astra_notification_batches (
    posting_window_id varchar(36) PRIMARY KEY REFERENCES posting_windows(id) ON DELETE RESTRICT,
    user_ids text[] NOT NULL,
    notification_ids text[] NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (cardinality(user_ids)=cardinality(notification_ids))
);
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='isureal') THEN
        GRANT SELECT, INSERT, TRUNCATE ON astra_notification_batches TO isureal;
    END IF;
END $$;
