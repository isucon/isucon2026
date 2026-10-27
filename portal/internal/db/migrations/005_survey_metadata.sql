-- Empty strings mean unreported; do not infer human/manual for existing data.
ALTER TABLE survey_responses ADD COLUMN IF NOT EXISTS agent text NOT NULL DEFAULT '';
ALTER TABLE survey_responses ADD COLUMN IF NOT EXISTS harness text NOT NULL DEFAULT '';

-- One snapshot per benchmark job. Progress and completion update the same row.
CREATE TABLE IF NOT EXISTS survey_response_history (
    id bigserial PRIMARY KEY,
    team_id integer NOT NULL,
    benchmark_job_id integer NOT NULL UNIQUE,
    language varchar(255),
    agent text NOT NULL DEFAULT '',
    harness text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS index_survey_response_history_on_team_id_job_id
    ON survey_response_history (team_id, benchmark_job_id);

-- Only the last survey is available on upgrade; earlier values cannot be recovered.
INSERT INTO survey_response_history
    (team_id, benchmark_job_id, language, agent, harness, created_at, updated_at)
SELECT team_id, benchmark_job_id, language, agent, harness, created_at, updated_at
FROM survey_responses
WHERE team_id IS NOT NULL AND benchmark_job_id IS NOT NULL AND language IS NOT NULL AND language <> ''
ON CONFLICT (benchmark_job_id) DO NOTHING;
