CREATE TABLE benchmark_validation_snapshots (
 job_id bigint PRIMARY KEY REFERENCES benchmark_jobs(id) ON DELETE CASCADE,
 data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE benchmark_jobs ADD COLUMN validation_source_job_id bigint REFERENCES benchmark_jobs(id);
