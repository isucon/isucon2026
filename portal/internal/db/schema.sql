-- isucon14-portal db/schema.rb (version 2024_11_30_135122) の 16 テーブルをそのまま写した初期スキーマ。
-- 既存カラム・インデックスの変更禁止、追加は可（CT-8）。

CREATE TABLE IF NOT EXISTS benchmark_executions (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS benchmark_jobs (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int NOT NULL,
  status int NOT NULL DEFAULT 0,
  instance_name varchar(255),
  handle varchar(255),
  started_at datetime(6),
  finished_at datetime(6),
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  target_id int,
  enqueued_by_id int,
  post_validation tinyint(1) DEFAULT 0,
  KEY index_benchmark_jobs_on_instance_name_and_id (instance_name, id),
  KEY index_benchmark_jobs_on_status_and_team_id_and_id (status, team_id, id),
  KEY index_benchmark_jobs_on_team_id_and_id (team_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS benchmark_results (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int NOT NULL,
  benchmark_job_id int NOT NULL,
  score int NOT NULL DEFAULT 0,
  score_raw int NOT NULL DEFAULT 0,
  score_deduction int NOT NULL DEFAULT 0,
  finished tinyint(1) NOT NULL,
  passed tinyint(1),
  marked_at datetime(6) NOT NULL,
  reason text,
  stdout text,
  stderr text,
  exit_status int,
  exit_signal int,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  UNIQUE KEY index_benchmark_results_on_benchmark_job_id (benchmark_job_id),
  KEY idx_leaderboard (finished, exit_status, exit_signal, marked_at, team_id),
  KEY idx_leaderboard2 (finished, exit_status, exit_signal, team_id, marked_at, id),
  KEY index_benchmark_results_on_passed (passed),
  KEY index_benchmark_results_on_team_id_and_updated_at (team_id, updated_at),
  KEY index_benchmark_results_on_team_id (team_id),
  KEY index_benchmark_results_on_updated_at (updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS clarifications (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int,
  disclosed tinyint(1),
  question text,
  answer text,
  original_question text,
  answered_at datetime(6),
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  admin tinyint(1) NOT NULL DEFAULT 0,
  KEY index_clarifications_on_disclosed_and_created_at (disclosed, created_at),
  KEY index_clarifications_on_team_id_and_created_at (team_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS contestant_instances (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int NOT NULL,
  cloud_id varchar(255) NOT NULL,
  number int NOT NULL,
  status int NOT NULL,
  private_ipv4_address varchar(255) NOT NULL,
  public_ipv4_address varchar(255),
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  KEY index_contestant_instances_on_cloud_id (cloud_id),
  KEY index_contestant_instances_on_private_ipv4_address (private_ipv4_address),
  KEY index_contestant_instances_on_status (status),
  UNIQUE KEY index_contestant_instances_on_team_id_and_number (team_id, number)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS contestants (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int NOT NULL,
  name varchar(255) NOT NULL,
  github_login varchar(255) NOT NULL,
  discord_id varchar(255) NOT NULL,
  avatar_url varchar(255) NOT NULL,
  student tinyint(1) NOT NULL DEFAULT 0,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  github_id varchar(255) NOT NULL,
  discord_tag varchar(255) NOT NULL,
  is_discord_guild_member tinyint(1) NOT NULL DEFAULT 0,
  in_person tinyint(1),
  UNIQUE KEY index_contestants_on_discord_id (discord_id),
  UNIQUE KEY index_contestants_on_github_id (github_id),
  KEY index_contestants_on_team_id (team_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS coupons (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int NOT NULL,
  code varchar(255) NOT NULL,
  activate tinyint(1) NOT NULL DEFAULT 0,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS env_checks (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int NOT NULL,
  name varchar(255) NOT NULL,
  ip_address varchar(255) NOT NULL,
  passed tinyint(1) NOT NULL,
  message text NOT NULL,
  admin_message text NOT NULL,
  raw_data text NOT NULL,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS extra_time_assignments (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id bigint NOT NULL,
  final tinyint(1) NOT NULL,
  seconds int NOT NULL,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  UNIQUE KEY index_extra_time_assignments_on_team_id_and_final (team_id, final),
  KEY index_extra_time_assignments_on_team_id (team_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS instance_command_execute_request_results (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  instance_command_execute_request_id int NOT NULL,
  contestant_instance_id int NOT NULL,
  output text,
  exit_code int,
  finished_at datetime(6),
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  UNIQUE KEY index_command_execute_results_on_request_id_and_ci_id (instance_command_execute_request_id, contestant_instance_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS instance_command_execute_requests (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  command text,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS notifications (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  contestant_id int NOT NULL,
  `read` tinyint(1) NOT NULL DEFAULT 0,
  encoded_message text NOT NULL,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  KEY index_notifications_on_contestant_id_and_id (contestant_id, id),
  KEY index_notifications_on_contestant_id_and_read_and_id (contestant_id, `read`, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS push_subscriptions (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  contestant_id int NOT NULL,
  endpoint varchar(255) NOT NULL,
  p256dh varchar(255) NOT NULL,
  auth varchar(255) NOT NULL,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  KEY index_push_subscriptions_on_contestant_id_and_endpoint (contestant_id, endpoint)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS ssh_public_keys (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  contestant_id int NOT NULL,
  public_key text NOT NULL,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  KEY index_ssh_public_keys_on_contestant_id (contestant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS survey_responses (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  team_id int,
  benchmark_job_id int,
  language varchar(255),
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  KEY index_survey_responses_on_benchmark_job_id (benchmark_job_id),
  KEY index_survey_responses_on_team_id (team_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS teams (
  id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name varchar(255) NOT NULL,
  leader_id int,
  is_hidden tinyint(1) NOT NULL DEFAULT 0,
  final_participation tinyint(1) NOT NULL DEFAULT 0,
  email_address varchar(255) NOT NULL,
  invite_token varchar(255) NOT NULL,
  created_at datetime(6) NOT NULL,
  updated_at datetime(6) NOT NULL,
  withdrawn tinyint(1) NOT NULL DEFAULT 0,
  disqualified tinyint(1) NOT NULL DEFAULT 0,
  student tinyint(1) NOT NULL,
  availability_zone varchar(255) DEFAULT 'apne1-az2',
  best_benchmark_result_id bigint,
  best_benchmark_result_for_audience_id bigint,
  best_benchmark_result_for_admin_id bigint,
  KEY index_teams_on_best_benchmark_result_for_admin_id (best_benchmark_result_for_admin_id),
  KEY index_teams_on_best_benchmark_result_for_audience_id (best_benchmark_result_for_audience_id),
  KEY index_teams_on_best_benchmark_result_id (best_benchmark_result_id),
  KEY idx_active_final (withdrawn, disqualified, final_participation)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
