-- The same (user, window) pair remains unique. Leading with the window keeps
-- each bulk notification insert on a small set of nearby index pages instead
-- of touching historical leaves for every recipient. User/time polling keeps
-- its separate astra_notifications_user_page index.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass
      AND conname = 'notifications_user_id_posting_window_id_key'
      AND pg_get_constraintdef(oid) = 'UNIQUE (user_id, posting_window_id)'
  ) THEN
    ALTER TABLE notifications
      DROP CONSTRAINT notifications_user_id_posting_window_id_key,
      ADD CONSTRAINT notifications_user_id_posting_window_id_key
        UNIQUE (posting_window_id, user_id);
  ELSIF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass
      AND conname = 'notifications_user_id_posting_window_id_key'
      AND pg_get_constraintdef(oid) = 'UNIQUE (posting_window_id, user_id)'
  ) THEN
    RAISE EXCEPTION 'Unexpected notifications uniqueness constraint; inspect before migration';
  END IF;
END $$;
