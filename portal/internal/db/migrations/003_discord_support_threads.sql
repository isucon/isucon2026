-- Discord transcripts are staff-only and must never be copied into clarifications.answer.
CREATE TABLE IF NOT EXISTS discord_support_threads (
 clarification_id bigint PRIMARY KEY REFERENCES clarifications(id),
 thread_id text UNIQUE,
 participant_ids jsonb NOT NULL,
 status text NOT NULL CHECK (status IN ('opening','open','closing','closed')),
 transcript jsonb NOT NULL DEFAULT '[]',
 transcript_saved_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 closed_at timestamptz
);
