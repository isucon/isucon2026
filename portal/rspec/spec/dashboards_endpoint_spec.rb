# 実エンドポイント（/api/{audience,contestant,admin}/dashboard）の受け入れテスト。
# /debug/leaderboard と同じエンジンが production ルートで正しく配線されていることと、
# Content-Type（CT-1）・ETag/304（CT-5）・ガード・追加時間（FR-S-11 / DM-9）を確認する。

RSpec.describe 'dashboard endpoints' do
  let!(:now) { Time.now }
  let!(:team1) { create(:team) }
  let!(:team2) { create(:team) }

  before do
    @timing = {
      contest_start: now - 3600,
      contest_freeze: now + 60,
      contest_end: now + 120,
    }
  end

  describe 'GET /api/audience/dashboard' do
    before do
      create(:benchmark_job, :passed, scored: 100, team: team1, marked_at: now)
      create(:benchmark_job, :passed, scored: 200, team: team2, marked_at: now + 1)
      create(:benchmark_job, :passed, scored: 900, team: team2, marked_at: now + 61) # 凍結後
      portal_update_best_scores
    end

    it 'returns audience leaderboard without history' do
      res = portal_get('/api/audience/dashboard', {}, timing_headers)
      expect(res.code).to eq('200')
      expect(res['content-type']).to eq('application/protobuf; proto=isuxportal.proto.services.audience.DashboardResponse')

      lb = Isuxportal::Proto::Services::Audience::DashboardResponse.decode(res.body).leaderboard
      expect(lb.teams.map { |t| t.team.id }).to eq([team2.id, team1.id])
      expect(lb.teams[0].latest_score.score).to eq(200) # 凍結後の 900 は見えない
      expect(lb.teams[0].best_score.score).to eq(200)
      expect(lb.teams[0].score_history).to be_nil
      expect(lb.contest.frozen).to eq(false)
    end

    it 'serves 304 with If-None-Match' do
      res = portal_get('/api/audience/dashboard', {}, timing_headers)
      expect(res.code).to eq('200')
      etag = res['etag']
      expect(etag).not_to be_nil

      res2 = portal_get('/api/audience/dashboard', {}, timing_headers('If-None-Match' => etag))
      expect(res2.code).to eq('304')

      create(:benchmark_job, :passed, scored: 300, team: team1, marked_at: now + 2)
      res3 = portal_get('/api/audience/dashboard', {}, timing_headers('If-None-Match' => etag))
      expect(res3.code).to eq('200')
    end
  end

  describe 'GET /api/audience/dashboard/teams/:id' do
    it 'returns solo item with history' do
      create(:benchmark_job, :passed, scored: 100, team: team1, marked_at: now)
      create(:benchmark_job, :passed, scored: 150, team: team1, marked_at: now + 1)
      portal_update_best_scores

      res = portal_get("/api/audience/dashboard/teams/#{team1.id}", {}, timing_headers)
      expect(res.code).to eq('200')
      item = Isuxportal::Proto::Services::Audience::SoloDashboardResponse.decode(res.body).leaderboard_item
      expect(item.team.id).to eq(team1.id)
      expect(item.score_history.scores.map(&:score)).to eq([100, 150])
      expect(item.best_score.score).to eq(150)
    end

    it 'returns 404 for unknown team' do
      res = portal_get('/api/audience/dashboard/teams/999999', {}, timing_headers)
      expect(res.code).to eq('404')
      err = Isuxportal::Proto::Error.decode(res.body)
      expect(err.code).to eq(404)
    end
  end

  describe 'GET /api/contestant/dashboard' do
    let(:session) { { 'X-Isux-Session-Team-Id' => team1.id.to_s } }

    it 'returns own leaderboard item with history, including post-freeze own results' do
      create(:benchmark_job, :passed, scored: 100, team: team1, marked_at: now)
      create(:benchmark_job, :passed, scored: 500, team: team1, marked_at: now + 61) # 自チームの凍結後
      portal_update_best_scores

      res = portal_get('/api/contestant/dashboard', {}, timing_headers(session))
      expect(res.code).to eq('200')
      item = Isuxportal::Proto::Services::Contestant::DashboardResponse.decode(res.body).leaderboard_item
      expect(item.team.id).to eq(team1.id)
      expect(item.score_history.scores.map(&:score)).to eq([100, 500])
      expect(item.best_score.score).to eq(500)
    end

    it 'redirects anonymous access to GitHub login (Rails 互換)' do
      res = portal_get('/api/contestant/dashboard', {}, timing_headers)
      expect(res.code).to eq('302')
      expect(res['location']).to start_with('/session/new/github?')
    end

    it 'returns 404 after contest end' do
      @timing = { contest_start: now - 7200, contest_freeze: now - 3660, contest_end: now - 3600 }
      res = portal_get('/api/contestant/dashboard', {}, timing_headers(session))
      expect(res.code).to eq('404')
    end

    it 'stays open with extra time assignment (final=false)' do
      @timing = { contest_start: now - 7200, contest_freeze: now - 3660, contest_end: now - 3600 }
      ExtraTimeAssignment.create!(team: team1, final: false, seconds: 7200)
      res = portal_get('/api/contestant/dashboard', {}, timing_headers(session))
      expect(res.code).to eq('200')
    end
  end

  describe 'GET /api/admin/dashboard' do
    it 'redirects non-staff to admin login (Rails 互換)' do
      res = portal_get('/api/admin/dashboard', {}, timing_headers)
      expect(res.code).to eq('302')
      expect(res['location']).to start_with('/admin/session/new?')
    end

    it 'returns admin leaderboard with unanswered clarification count' do
      create(:benchmark_job, :passed, scored: 100, team: team1, marked_at: now)
      create(:benchmark_job, :passed, scored: 900, team: team2, marked_at: now + 61) # 凍結後も admin には見える
      portal_update_best_scores
      Clarification.create!(team: team1, question: 'q', admin: false)

      res = portal_get('/api/admin/dashboard', {}, timing_headers('X-Isux-Session-Staff' => '1'))
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Admin::DashboardResponse.decode(res.body)
      expect(body.unanswered_clarification_count).to eq(1)
      expect(body.leaderboard.teams.map { |t| t.team.id }).to eq([team2.id, team1.id])
      expect(body.leaderboard.teams[0].latest_score.score).to eq(900)
    end
  end
end
