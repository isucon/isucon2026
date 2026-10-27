-- Preserve comma-separated model names as reported. Empty means manual or unreported.
ALTER TABLE survey_responses ADD COLUMN IF NOT EXISTS model text NOT NULL DEFAULT '';
ALTER TABLE survey_response_history ADD COLUMN IF NOT EXISTS model text NOT NULL DEFAULT '';
