# 運営 API（FR-S）と運営画面（ERB 相当 3 画面）の受け入れテスト。

RSpec.describe 'admin API' do
  let!(:now) { Time.now }
  let!(:team1) { create(:team) }
  let!(:team2) { create(:team, :student) }

  before do
    @timing = { contest_start: now - 3600, contest_freeze: now + 60, contest_end: now + 120 }
  end

  def staff_headers(extra = {})
    timing_headers({ 'X-Isux-Session-Staff' => '1' }.merge(extra))
  end

  describe 'teams' do
    it 'lists all teams including hidden/withdrawn (FR-S-2)' do
      res = portal_get('/api/admin/teams', {}, staff_headers)
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Admin::ListTeamsResponse.decode(res.body)
      expect(body.teams.map(&:team_id)).to eq([team1.id, team2.id])
      expect(body.teams[1].is_student).to eq(true)
      expect(body.teams[0].member_names).to eq(team1.members.map(&:name))
    end

    it 'shows team detail with members and invite token' do
      res = portal_get("/api/admin/teams/#{team1.id}", {}, staff_headers)
      body = Isuxportal::Proto::Services::Admin::GetTeamResponse.decode(res.body)
      expect(body.team.detail.invite_token).to eq(team1.invite_token)
      expect(body.team.members.size).to eq(1)
      expect(body.team.leader.id).to eq(team1.leader.id)
    end

    it 'updates team flags and specified contestants only' do
      leader = team1.members.first
      req = Isuxportal::Proto::Services::Admin::UpdateTeamRequest.new(
        team: Isuxportal::Proto::Resources::Team.new(
          id: team1.id, name: 'Renamed', leader_id: leader.id,
          final_participation: true, hidden: true, withdrawn: false, disqualified: true,
          detail: Isuxportal::Proto::Resources::Team::TeamDetail.new(email_address: 'new@test.invalid'),
        ),
        contestants: [
          Isuxportal::Proto::Resources::Contestant.new(
            id: leader.id, name: 'Renamed Member',
            detail: Isuxportal::Proto::Resources::Contestant::ContestantDetail.new(
              is_student: true, github_id: leader.github_id, github_login: leader.github_login,
              discord_id: leader.discord_id, discord_tag: leader.discord_tag,
            ),
          ),
        ],
      )
      res = portal_put("/api/admin/teams/#{team1.id}", staff_headers,
                       body: Isuxportal::Proto::Services::Admin::UpdateTeamRequest.encode(req))
      expect(res.code).to eq('200')
      team1.reload
      expect(team1.name).to eq('Renamed')
      expect(team1.is_hidden).to eq(true)
      expect(team1.disqualified).to eq(true)
      expect(leader.reload.name).to eq('Renamed Member')
      expect(team1.student).to eq(true) # メンバー全員 student に変わったため導出（DM-5）
    end

    it 'rejects mismatched id with 400' do
      req = Isuxportal::Proto::Services::Admin::UpdateTeamRequest.new(
        team: Isuxportal::Proto::Resources::Team.new(
          id: team2.id, name: 'x',
          detail: Isuxportal::Proto::Resources::Team::TeamDetail.new(email_address: 'x@test.invalid'),
        ),
      )
      res = portal_put("/api/admin/teams/#{team1.id}", staff_headers,
                       body: Isuxportal::Proto::Services::Admin::UpdateTeamRequest.encode(req))
      expect(res.code).to eq('400')
    end
  end

  describe 'benchmark jobs' do
    let!(:instance1) { create(:contestant_instance, team: team1, number: 1) }

    it 'enqueues on behalf of a team by target number and cancels it (FR-S-3)' do
      req = Isuxportal::Proto::Services::Admin::EnqueueBenchmarkJobRequest.new(team_id: team1.id, target_number: 1)
      res = portal_post('/api/admin/benchmark_jobs', {}, staff_headers,
                        body: Isuxportal::Proto::Services::Admin::EnqueueBenchmarkJobRequest.encode(req))
      expect(res.code).to eq('200')
      job = Isuxportal::Proto::Services::Admin::EnqueueBenchmarkJobResponse.decode(res.body).job
      expect(job.target_id).to eq(instance1.id)
      expect(job.instance_name).to eq(team1.availability_zone)

      res = portal_delete("/api/admin/benchmark_jobs/#{job.id}", staff_headers)
      expect(res.code).to eq('200')
      expect(BenchmarkJob.find(job.id).status).to eq('cancelled')

      # 完了済みジョブの再キャンセルは 404
      res = portal_delete("/api/admin/benchmark_jobs/#{job.id}", staff_headers)
      expect(res.code).to eq('404')
    end

    it 'filters and paginates job list' do
      create(:benchmark_job, :passed, scored: 100, team: team1)
      create(:benchmark_job, :failed, scored: 100, team: team2)

      res = portal_get('/api/admin/benchmark_jobs', { 'failed_only' => '1' }, staff_headers)
      body = Isuxportal::Proto::Services::Admin::ListBenchmarkJobsResponse.decode(res.body)
      expect(body.jobs.size).to eq(1)
      expect(body.jobs[0].team_id).to eq(team2.id)
      expect(body.max_page).to eq(1)

      res = portal_get('/api/admin/benchmark_jobs', { 'team_id' => team1.id.to_s }, staff_headers)
      body = Isuxportal::Proto::Services::Admin::ListBenchmarkJobsResponse.decode(res.body)
      expect(body.jobs.map(&:team_id)).to eq([team1.id])
    end
  end

  describe 'clarifications (FR-S-4)' do
    it 'creates a global announcement and notifies all contestants' do
      req = Isuxportal::Proto::Services::Admin::CreateClarificationRequest.new(
        question: 'announce q', answer: 'announce a', team_id: 0,
      )
      res = portal_post('/api/admin/clarifications', {}, staff_headers,
                        body: Isuxportal::Proto::Services::Admin::CreateClarificationRequest.encode(req))
      expect(res.code).to eq('200')
      clar = Isuxportal::Proto::Services::Admin::CreateClarificationResponse.decode(res.body).clarification
      expect(clar.disclosed).to eq(true)
      expect(clar.admin).to eq(true)

      # 全選手（2 チーム × 1 人）に通知が一括挿入される（NF-4）
      expect(Notification.count).to eq(2)
    end

    it 'responds to a contestant clarification and notifies the team' do
      clar = Clarification.create!(team: team1, question: 'q', original_question: 'q', admin: false)
      req = Isuxportal::Proto::Services::Admin::RespondClarificationRequest.new(
        id: clar.id, disclose: false, answer: 'here you go', question: '',
      )
      res = portal_put("/api/admin/clarifications/#{clar.id}", staff_headers,
                       body: Isuxportal::Proto::Services::Admin::RespondClarificationRequest.encode(req))
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Admin::RespondClarificationResponse.decode(res.body).clarification
      expect(body.answered).to eq(true)
      expect(body.answer).to eq('here you go')
      expect(body.question).to eq('q') # 上書きなしなら original を維持

      # 当該チームのメンバーにだけ通知
      expect(Notification.count).to eq(1)
      expect(Notification.first.contestant_id).to eq(team1.members.first.id)
    end

    it 'lists with unanswered_only filter' do
      Clarification.create!(team: team1, question: 'q1', original_question: 'q1', admin: false)
      Clarification.create!(team: team2, question: 'q2', original_question: 'q2', admin: false, answered_at: now, answer: 'a')
      res = portal_get('/api/admin/clarifications', { 'unanswered_only' => '1' }, staff_headers)
      body = Isuxportal::Proto::Services::Admin::ListClarificationsResponse.decode(res.body)
      expect(body.clarifications.size).to eq(1)
      expect(body.clarifications[0].question).to eq('q1')
    end
  end

  describe 'dump_leaderboard (FR-S-9)' do
    it 'dumps standings at contest-end' do
      create(:benchmark_job, :passed, scored: 100, team: team1, marked_at: now)
      create(:benchmark_job, :passed, scored: 200, team: team2, marked_at: now + 1)
      create(:benchmark_job, :passed, scored: 900, team: team2, marked_at: now + 300) # 終了後
      portal_update_best_scores

      res = portal_get('/api/admin/dump_leaderboard', { 'when' => 'contest-end' }, staff_headers)
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Admin::GetLeaderboardDumpResponse.decode(res.body)
      expect(body.items.map { |i| [i.position, i.team.id] }).to eq([[1, team2.id], [2, team1.id]])
      expect(body.items[0].best_score.score).to eq(200) # 終了後の 900 は含まない
    end

    it 'rejects blank/invalid when' do
      expect(portal_get('/api/admin/dump_leaderboard', {}, staff_headers).code).to eq('400')
      expect(portal_get('/api/admin/dump_leaderboard', { 'when' => 'not-a-time' }, staff_headers).code).to eq('400')
    end
  end

  describe 'unprepared stats (FR-S-8)' do
    it 'reports members without ssh keys / discord / env checks' do
      res = portal_get('/api/admin/unprepared_stats/ssh_key_stats', {}, staff_headers)
      body = Isuxportal::Proto::Services::Admin::GetSSHKeyStatsResponse.decode(res.body)
      expect(body.items.map { |i| i.team.id }).to eq([team1.id, team2.id])

      SshPublicKey.create!(contestant_id: team1.members.first.id, public_key: 'k')
      res = portal_get('/api/admin/unprepared_stats/ssh_key_stats', {}, staff_headers)
      body = Isuxportal::Proto::Services::Admin::GetSSHKeyStatsResponse.decode(res.body)
      expect(body.items.map { |i| i.team.id }).to eq([team2.id])

      res = portal_get('/api/admin/unprepared_stats/discord_stats', {}, staff_headers)
      body = Isuxportal::Proto::Services::Admin::GetDiscordStatsResponse.decode(res.body)
      expect(body.items.size).to eq(2)

      EnvCheck.create!(team_id: team1.id, name: 'test-ssh', passed: true, ip_address: '1.2.3.4',
                       message: 'm', admin_message: 'am', raw_data: '{}')
      res = portal_get('/api/admin/unprepared_stats/env_check_stats', {}, staff_headers)
      body = Isuxportal::Proto::Services::Admin::GetEnvCheckStatsResponse.decode(res.body)
      expect(body.items.map(&:id)).to eq([team2.id])
    end
  end

  describe 'last validations (FR-S-7)' do
    it 'creates command execute request rows for trigger_env_check' do
      create(:contestant_instance, team: team1, number: 1)
      create(:contestant_instance, team: team2, number: 1)
      req = Isuxportal::Proto::Services::Admin::TriggerEnvCheckRequest.new(team_ids: [team1.id])
      res = portal_post('/api/admin/last_validations/env_check', {}, staff_headers,
                        body: Isuxportal::Proto::Services::Admin::TriggerEnvCheckRequest.encode(req))
      expect(res.code).to eq('200')

      res = portal_get('/api/admin/instance_command_execute_requests', {}, staff_headers)
      body = Isuxportal::Proto::Services::Admin::ListInstanceCommandExecuteRequestsResponse.decode(res.body)
      expect(body.requests.size).to eq(1)
      expect(body.requests[0].command).to eq('sudo systemctl restart envcheck; status=$?; sudo cat /var/lib/isucon-envcheck/result.json; exit "$status"')
      expect(body.requests[0].total_target_count).to eq(2)
    end

    it 'requeues benchmarks with post_validation carrying over last target' do
      instance = create(:contestant_instance, team: team1, number: 1)
      job = create(:benchmark_job, :passed, scored: 100, team: team1)
      job.update!(target_id: instance.id)
      ActiveRecord::Base.connection.execute(
        "INSERT INTO benchmark_validation_snapshots(job_id, data) VALUES (#{job.id}, '{\"version\":1,\"users\":[{}]}')"
      )

      req = Isuxportal::Proto::Services::Admin::TriggerBenchmarksRequest.new(team_ids: [team1.id, team2.id], post_validation: true)
      res = portal_post('/api/admin/last_validations/benchmarks', {}, staff_headers,
                        body: Isuxportal::Proto::Services::Admin::TriggerBenchmarksRequest.encode(req))
      expect(res.code).to eq('200')

      new_job = BenchmarkJob.where(team_id: team1.id).order(id: :desc).first
      expect(new_job.post_validation).to eq(true)
      expect(new_job.target_id).to eq(instance.id)
      expect(new_job.status).to eq('pending')
      expect(BenchmarkJob.where(team_id: team2.id).count).to eq(0) # 直近ジョブが無いチームはスキップ
    end
  end

  describe 'admin pages' do
    it 'logs in via form with constant-time comparison (FR-AUTH-8)' do
      res = portal_get('/admin/session/new')
      expect(res.code).to eq('200')
      csrf = res.body.match(/name="authenticity_token" value="([^"]+)"/)[1]

      res = portal_post('/admin/session', {}, {}, form: { 'login' => 'admin', 'password' => 'wrong', 'authenticity_token' => csrf })
      expect(res.code).to eq('302')
      expect(res['location']).to include('error=1')

      res = portal_post('/admin/session', {}, {}, form: { 'login' => 'admin', 'password' => 'adminpass', 'authenticity_token' => csrf })
      expect(res.code).to eq('302')
      expect(res['location']).to eq('/admin')

      # ログイン後は admin API に到達できる
      res = portal_get('/api/admin/teams', {}, timing_headers)
      expect(res.code).to eq('200')
    end

    it 'issues bypass token from the factory page (FR-S-10) and Go-issued token verifies' do
      res = portal_get('/admin/session/new')
      csrf = res.body.match(/name="authenticity_token" value="([^"]+)"/)[1]
      portal_post('/admin/session', {}, {}, form: { 'login' => 'admin', 'password' => 'adminpass', 'authenticity_token' => csrf })

      res = portal_get('/admin/bypass_token')
      expect(res.code).to eq('200')
      csrf = res.body.match(/name="authenticity_token" value="([^"]+)"/)[1]
      res = portal_post('/admin/bypass_token', {}, {}, form: {
        'expires_in' => '3600', 'usages[]' => 'CREATE_TEAM', 'authenticity_token' => csrf,
      })
      expect(res.code).to eq('200')
      url = res.body.match(%r{id="bypass-token-url">([^<]+)<})[1]
      token = url.split('/session/bypass/').last

      # Go 発行のトークンを Ruby 側でも検証できる（相互運用、FR-AUTH-7）
      sig_b64, payload_b64 = token.split(':', 2)
      pad = ->(s) { s.tr('-_', '+/') + '=' * ((4 - s.size % 4) % 4) }
      payload_bin = pad.call(payload_b64).unpack1('m*')
      expected_sig = OpenSSL::HMAC.digest('sha384', PortalClient::BYPASS_SECRET, payload_bin)
      expect(pad.call(sig_b64).unpack1('m*')).to eq(expected_sig)
      payload = Isuxportal::Proto::Misc::BypassTokenPayload.decode(payload_bin)
      expect(payload.usages).to eq([:CREATE_TEAM])
      expect(payload.expiry).to be > Time.now.to_i

      # そのまま /session/bypass で使える
      res = portal_get("/session/bypass/#{token}")
      expect(res.code).to eq('302')
    end

    it 'impersonates a contestant (FR-AUTH-9)' do
      res = portal_get('/admin/session/new')
      csrf = res.body.match(/name="authenticity_token" value="([^"]+)"/)[1]
      portal_post('/admin/session', {}, {}, form: { 'login' => 'admin', 'password' => 'adminpass', 'authenticity_token' => csrf })

      leader = team1.members.first
      res = portal_post('/admin/impersonate/contestant', {}, {}, form: { 'id' => leader.id.to_s, 'authenticity_token' => csrf })
      expect(res.code).to eq('302')

      res = portal_get('/api/session', {}, timing_headers)
      me = Isuxportal::Proto::Services::Common::GetCurrentSessionResponse.decode(res.body)
      expect(me.contestant.id).to eq(leader.id)
    end
  end

  describe 'debug endpoints (FR-S-12)' do
    it 'long_running_check is reachable without staff (notifyloop 用)' do
      res = portal_get('/admin/long_running_check')
      expect(res.code).to eq('200')
      expect(res.body).to eq('enqueud')
    end

    it 'slacktown requires staff' do
      res = portal_get('/admin/slacktown')
      expect(res.code).to eq('302')
      res = portal_get('/admin/slacktown', {}, 'X-Isux-Session-Staff' => '1')
      expect(res.code).to eq('200')
      expect(res.body).to eq('slacktown')
    end
  end
end
