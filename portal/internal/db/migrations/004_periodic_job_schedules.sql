-- Durable scheduling gates prevent duplicate cycles across worker restarts.
CREATE TABLE IF NOT EXISTS periodic_job_schedules (
    name text PRIMARY KEY,
    next_run_at timestamptz NOT NULL
);
