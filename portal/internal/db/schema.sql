-- isucon14-portal db/schema.rb (version 2024_11_30_135122) の 16 テーブルを PostgreSQL に移した初期スキーマ。
-- 2026-09-14 の運営判断（Rails へのロールバックは不要）により MySQL から PostgreSQL へ移設。
-- 型の対応: datetime(6) → timestamptz, tinyint(1) → boolean, auto_increment → bigserial。
-- 追加: jobs（SQS の代替キュー）、survey_responses.team_id の一意制約、
--       benchmark_jobs の部分一意インデックス（DM-4: チームあたり pending/running は 1 件）。

CREATE TABLE IF NOT EXISTS benchmark_executions (
  id bigserial PRIMARY KEY,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS benchmark_jobs (
  id bigserial PRIMARY KEY,
  team_id integer NOT NULL,
  status integer NOT NULL DEFAULT 0,
  instance_name varchar(255),
  handle varchar(255),
  started_at timestamptz,
  finished_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  target_id integer,
  enqueued_by_id integer,
  post_validation boolean DEFAULT false
);
CREATE INDEX IF NOT EXISTS index_benchmark_jobs_on_instance_name_and_id ON benchmark_jobs (instance_name, id);
CREATE INDEX IF NOT EXISTS index_benchmark_jobs_on_status_and_team_id_and_id ON benchmark_jobs (status, team_id, id);
CREATE INDEX IF NOT EXISTS index_benchmark_jobs_on_team_id_and_id ON benchmark_jobs (team_id, id);
-- DM-4: 同時に pending(0) / running(1) のジョブはチームあたり 1 件（宣言的に保証）
CREATE UNIQUE INDEX IF NOT EXISTS index_benchmark_jobs_one_active_per_team ON benchmark_jobs (team_id) WHERE status IN (0, 1);

CREATE TABLE IF NOT EXISTS benchmark_results (
  id bigserial PRIMARY KEY,
  team_id integer NOT NULL,
  benchmark_job_id integer NOT NULL,
  score integer NOT NULL DEFAULT 0,
  score_raw integer NOT NULL DEFAULT 0,
  score_deduction integer NOT NULL DEFAULT 0,
  finished boolean NOT NULL,
  passed boolean,
  marked_at timestamptz NOT NULL,
  reason text,
  stdout text,
  stderr text,
  exit_status integer,
  exit_signal integer,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS index_benchmark_results_on_benchmark_job_id ON benchmark_results (benchmark_job_id);
CREATE INDEX IF NOT EXISTS idx_leaderboard ON benchmark_results (finished, exit_status, exit_signal, marked_at, team_id);
CREATE INDEX IF NOT EXISTS idx_leaderboard2 ON benchmark_results (finished, exit_status, exit_signal, team_id, marked_at, id);
CREATE INDEX IF NOT EXISTS index_benchmark_results_on_passed ON benchmark_results (passed);
CREATE INDEX IF NOT EXISTS index_benchmark_results_on_team_id_and_updated_at ON benchmark_results (team_id, updated_at);
CREATE INDEX IF NOT EXISTS index_benchmark_results_on_team_id ON benchmark_results (team_id);
CREATE INDEX IF NOT EXISTS index_benchmark_results_on_updated_at ON benchmark_results (updated_at);

CREATE TABLE IF NOT EXISTS clarifications (
  id bigserial PRIMARY KEY,
  team_id integer,
  disclosed boolean,
  question text,
  answer text,
  original_question text,
  answered_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  admin boolean NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS index_clarifications_on_disclosed_and_created_at ON clarifications (disclosed, created_at);
CREATE INDEX IF NOT EXISTS index_clarifications_on_team_id_and_created_at ON clarifications (team_id, created_at);

CREATE TABLE IF NOT EXISTS contestant_instances (
  id bigserial PRIMARY KEY,
  team_id integer NOT NULL,
  cloud_id varchar(255) NOT NULL,
  number integer NOT NULL,
  status integer NOT NULL,
  private_ipv4_address varchar(255) NOT NULL,
  public_ipv4_address varchar(255),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS index_contestant_instances_on_cloud_id ON contestant_instances (cloud_id);
CREATE INDEX IF NOT EXISTS index_contestant_instances_on_private_ipv4_address ON contestant_instances (private_ipv4_address);
CREATE INDEX IF NOT EXISTS index_contestant_instances_on_status ON contestant_instances (status);
CREATE UNIQUE INDEX IF NOT EXISTS index_contestant_instances_on_team_id_and_number ON contestant_instances (team_id, number);

CREATE TABLE IF NOT EXISTS contestants (
  id bigserial PRIMARY KEY,
  team_id integer NOT NULL,
  name varchar(255) NOT NULL,
  github_login varchar(255) NOT NULL,
  discord_id varchar(255) NOT NULL,
  avatar_url varchar(255) NOT NULL,
  student boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  github_id varchar(255) NOT NULL,
  discord_tag varchar(255) NOT NULL,
  is_discord_guild_member boolean NOT NULL DEFAULT false,
  in_person boolean
);
CREATE UNIQUE INDEX IF NOT EXISTS index_contestants_on_discord_id ON contestants (discord_id) WHERE discord_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS index_contestants_on_github_id ON contestants (github_id);
CREATE INDEX IF NOT EXISTS index_contestants_on_team_id ON contestants (team_id);

CREATE TABLE IF NOT EXISTS coupons (
  id bigserial PRIMARY KEY,
  team_id integer NOT NULL,
  code varchar(255) NOT NULL,
  activate boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS env_checks (
  id bigserial PRIMARY KEY,
  team_id integer NOT NULL,
  name varchar(255) NOT NULL,
  ip_address varchar(255) NOT NULL,
  passed boolean NOT NULL,
  message text NOT NULL,
  admin_message text NOT NULL,
  raw_data text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS extra_time_assignments (
  id bigserial PRIMARY KEY,
  team_id bigint NOT NULL,
  final boolean NOT NULL,
  seconds integer NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS index_extra_time_assignments_on_team_id_and_final ON extra_time_assignments (team_id, final);
CREATE INDEX IF NOT EXISTS index_extra_time_assignments_on_team_id ON extra_time_assignments (team_id);

CREATE TABLE IF NOT EXISTS instance_command_execute_request_results (
  id bigserial PRIMARY KEY,
  instance_command_execute_request_id integer NOT NULL,
  contestant_instance_id integer NOT NULL,
  output text,
  exit_code integer,
  finished_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS index_command_execute_results_on_request_id_and_ci_id
  ON instance_command_execute_request_results (instance_command_execute_request_id, contestant_instance_id);

CREATE TABLE IF NOT EXISTS instance_command_execute_requests (
  id bigserial PRIMARY KEY,
  command text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications (
  id bigserial PRIMARY KEY,
  contestant_id integer NOT NULL,
  "read" boolean NOT NULL DEFAULT false,
  encoded_message text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS index_notifications_on_contestant_id_and_id ON notifications (contestant_id, id);
CREATE INDEX IF NOT EXISTS index_notifications_on_contestant_id_and_read_and_id ON notifications (contestant_id, "read", id);

CREATE TABLE IF NOT EXISTS push_subscriptions (
  id bigserial PRIMARY KEY,
  contestant_id integer NOT NULL,
  endpoint varchar(255) NOT NULL,
  p256dh varchar(255) NOT NULL,
  auth varchar(255) NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS index_push_subscriptions_on_contestant_id_and_endpoint ON push_subscriptions (contestant_id, endpoint);

CREATE TABLE IF NOT EXISTS ssh_public_keys (
  id bigserial PRIMARY KEY,
  contestant_id integer NOT NULL,
  public_key text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS index_ssh_public_keys_on_contestant_id ON ssh_public_keys (contestant_id);

CREATE TABLE IF NOT EXISTS survey_responses (
  id bigserial PRIMARY KEY,
  team_id integer,
  benchmark_job_id integer,
  language varchar(255),
  agent text NOT NULL DEFAULT '',
  harness text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS index_survey_responses_on_benchmark_job_id ON survey_responses (benchmark_job_id);
-- OQ-3: survey_response はチームごとに 1 件で最新結果が上書きする。一意制約で upsert を成立させる
CREATE UNIQUE INDEX IF NOT EXISTS index_survey_responses_on_team_id ON survey_responses (team_id);

CREATE TABLE IF NOT EXISTS teams (
  id bigserial PRIMARY KEY,
  name varchar(255) NOT NULL,
  leader_id integer,
  is_hidden boolean NOT NULL DEFAULT false,
  final_participation boolean NOT NULL DEFAULT false,
  email_address varchar(255) NOT NULL,
  invite_token varchar(255) NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  withdrawn boolean NOT NULL DEFAULT false,
  disqualified boolean NOT NULL DEFAULT false,
  student boolean NOT NULL,
  availability_zone varchar(255) DEFAULT 'apne1-az2',
  best_benchmark_result_id bigint,
  best_benchmark_result_for_audience_id bigint,
  best_benchmark_result_for_admin_id bigint
);
CREATE INDEX IF NOT EXISTS index_teams_on_best_benchmark_result_for_admin_id ON teams (best_benchmark_result_for_admin_id);
CREATE INDEX IF NOT EXISTS index_teams_on_best_benchmark_result_for_audience_id ON teams (best_benchmark_result_for_audience_id);
CREATE INDEX IF NOT EXISTS index_teams_on_best_benchmark_result_id ON teams (best_benchmark_result_id);
CREATE INDEX IF NOT EXISTS idx_active_final ON teams (withdrawn, disqualified, final_participation);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_extra_time_assignments_team') THEN
    ALTER TABLE extra_time_assignments
      ADD CONSTRAINT fk_extra_time_assignments_team FOREIGN KEY (team_id) REFERENCES teams (id);
  END IF;
END $$;

-- ジョブキュー（SQS の代替、計画書 §1 からの変更）。
-- perform_later は同一トランザクションで INSERT し、cmd/worker が FOR UPDATE SKIP LOCKED で取り出す。
CREATE TABLE IF NOT EXISTS jobs (
  id bigserial PRIMARY KEY,
  name text NOT NULL,
  args jsonb NOT NULL,
  status text NOT NULL DEFAULT 'queued', -- queued | running | dead
  run_at timestamptz NOT NULL DEFAULT now(),
  locked_until timestamptz,
  attempts integer NOT NULL DEFAULT 0,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS index_jobs_claim ON jobs (status, run_at, id);
