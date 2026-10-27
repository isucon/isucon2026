# Go 実装向けの HTTP 越し受け入れテストのハーネス。
#
# isucon14-portal の contest_spec.rb（465 行）を転用する（計画書 §6）。
# - テストデータ作成: factory_bot + ActiveRecord（Go と同じ PostgreSQL に直接書く）
# - subject の取得: Rails の Contest.leaderboard 呼び出しの代わりに
#   TARGET_BASE のサーバへ HTTP リクエストし protobuf をデコードする
# - 競技時刻・セッション: Go サーバのテストモードヘッダ（X-Isux-*）で渡す

$LOAD_PATH.unshift File.expand_path('../lib', __dir__)

require 'net/http'
require 'time'
require 'securerandom'
require 'active_record'
require 'active_support/time'
require 'factory_bot'

require 'isuxportal/error_pb'
require 'isuxportal/misc/bypass_token_pb'
require 'isuxportal/resources/leaderboard_pb'
require 'isuxportal/resources/notification_pb'
require 'isuxportal/services/audience/dashboard_pb'
require 'isuxportal/services/audience/team_list_pb'
require 'isuxportal/services/contestant/dashboard_pb'
require 'isuxportal/services/contestant/benchmark_pb'
require 'isuxportal/services/contestant/clarifications_pb'
require 'isuxportal/services/contestant/notifications_pb'
require 'isuxportal/services/contestant/cloud_formation_pb'
require 'isuxportal/services/admin/dashboard_pb'
require 'isuxportal/services/admin/teams_pb'
require 'isuxportal/services/admin/benchmark_pb'
require 'isuxportal/services/admin/clarifications_pb'
require 'isuxportal/services/admin/env_checks_pb'
require 'isuxportal/services/admin/last_validations_pb'
require 'isuxportal/services/admin/leaderboard_dump_pb'
require 'isuxportal/services/admin/unprepared_stats_pb'
require 'isuxportal/services/admin/contestant_instances_pb'
require 'isuxportal/services/common/me_pb'
require 'isuxportal/services/common/storage_pb'
require 'isuxportal/services/registration/session_pb'
require 'isuxportal/services/registration/create_team_pb'
require 'isuxportal/services/registration/join_pb'
require 'isuxportal/services/registration/env_check_pb'
require 'isuxportal/services/registration/activate_coupon_pb'
require 'isuxportal/services/bench/receiving_pb'
require 'isuxportal/services/bench/reporting_pb'

Time.zone = 'UTC'

ActiveRecord::Base.establish_connection(
  adapter: 'postgresql',
  host: ENV.fetch('DB_HOST', '127.0.0.1'),
  port: Integer(ENV.fetch('DB_PORT', '15432')),
  username: ENV.fetch('DB_USER', 'isuxportal'),
  password: ENV.fetch('DB_PASSWORD', 'isuxportal'),
  database: ENV.fetch('DB_NAME', 'isuxportal'),
)

require_relative 'support/models'
require_relative 'support/portal_client'

TABLES_TO_CLEAN = %w[
  benchmark_jobs benchmark_results clarifications contestant_instances contestants
  coupons env_checks extra_time_assignments notifications push_subscriptions
  ssh_public_keys survey_response_history survey_responses teams
  instance_command_execute_requests instance_command_execute_request_results
  jobs registration_records registration_source_records
].freeze

def clean_database!
  ActiveRecord::Base.connection.execute("TRUNCATE TABLE #{TABLES_TO_CLEAN.join(', ')} RESTART IDENTITY CASCADE")
end

RSpec.configure do |config|
  config.include FactoryBot::Syntax::Methods
  config.include PortalClient

  config.expect_with :rspec do |expectations|
    expectations.include_chain_clauses_in_custom_matcher_descriptions = true
  end
  config.mock_with :rspec do |mocks|
    mocks.verify_partial_doubles = true
  end
  config.disable_monkey_patching!

  config.before(:suite) do
    FactoryBot.find_definitions
  end

  # Go サーバは別プロセスから DB を読むため、Rails のような
  # トランザクションロールバックは使えない。毎回 TRUNCATE + Redis flush する。
  config.before(:each) do
    clean_database!
    @cookies = {}
    portal_flushdb
  end
end
