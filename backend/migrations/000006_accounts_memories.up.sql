CREATE INDEX users_purge_due ON users(purge_due_at,id) WHERE status='deletion_pending';
CREATE INDEX posts_memories_month ON posts(author_id,posted_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX posting_windows_memory_date ON posting_windows(region,local_date,id);
CREATE INDEX image_objects_owner ON image_objects(owner_user_id,id);
