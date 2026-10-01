# factory_bot がテストデータを書くための最小限の ActiveRecord モデル。
# Rails 本体は使わない。isucon14-portal の app/models から、
# 「DB に書かれる行の内容」に影響するコールバックだけを写している。
# バリデーションは意図的に持たない（データは factory 由来で信頼できる）。

class ApplicationRecord < ActiveRecord::Base
  self.abstract_class = true
end

class Team < ApplicationRecord
  has_many :members, class_name: 'Contestant'
  has_one :leader, class_name: 'Contestant'
  has_many :benchmark_jobs
  has_many :benchmark_results
  has_many :contestant_instances
  has_many :extra_time_assignments

  before_validation :generate_invite_token
  before_validation :generate_student_status

  def members_all_student?
    members.size > 0 && members.all?(&:student)
  end

  def update_student_status
    self.student = members_all_student?
  end

  private def generate_invite_token
    self.invite_token ||= SecureRandom.urlsafe_base64(64)
  end

  private def generate_student_status
    update_student_status if student.nil?
  end
end

class Contestant < ApplicationRecord
  belongs_to :team

  # Contestant#save_team_student_status 相当（teams.student の導出。DM-5）
  after_save :save_team_student_status

  private def save_team_student_status
    if student
      team.update_student_status
      team.save!
    else
      team.update!(student: false)
    end
  end
end

class ContestantInstance < ApplicationRecord
  belongs_to :team
  enum :status, { unknown: 0, pending: 1, modifying: 2, stopped: 3, running: 4, terminated: 5 }

  # Rails 版と同じ: number 無しでは保存できない。
  # benchmark_job factory の target は number を持たないため、belongs_to の
  # autosave が黙って失敗し、benchmark_jobs.target_id は NULL のままになる
  # （原本 spec 実行時と同じ DB 状態）。
  validates :number, presence: true
end

class BenchmarkJob < ApplicationRecord
  belongs_to :team
  belongs_to :target, class_name: 'ContestantInstance', optional: true
  has_one :benchmark_result

  enum :status, { pending: 0, running: 1, errored: 2, cancelled: 3, finished: 4 }

  before_validation :generate_handle

  private def generate_handle
    if instance_name
      self.handle ||= SecureRandom.urlsafe_base64(64)
    end
  end
end

class BenchmarkResult < ApplicationRecord
  belongs_to :team, optional: true
  belongs_to :benchmark_job, optional: true

  before_validation :set_team_from_benchmark_job
  before_validation :set_score_zero_when_failed

  private def set_team_from_benchmark_job
    self.team = benchmark_job.team if benchmark_job
  end

  private def set_score_zero_when_failed
    if finished? && !passed?
      self.score = 0
    end
  end
end

class ExtraTimeAssignment < ApplicationRecord
  belongs_to :team
end

class Clarification < ApplicationRecord
  belongs_to :team, optional: true
end

class Notification < ApplicationRecord
  belongs_to :contestant
end

class PushSubscription < ApplicationRecord
  belongs_to :contestant
end

class SshPublicKey < ApplicationRecord
  belongs_to :contestant
end

class EnvCheck < ApplicationRecord
  belongs_to :team
end

class Coupon < ApplicationRecord
  belongs_to :team
end

class SurveyResponse < ApplicationRecord
  belongs_to :team, optional: true
end

class InstanceCommandExecuteRequest < ApplicationRecord
end

class InstanceCommandExecuteRequestResult < ApplicationRecord
  belongs_to :instance_command_execute_request
end
