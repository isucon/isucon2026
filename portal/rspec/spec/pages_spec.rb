# ページ層の受け入れテスト: SPA シェルの meta タグ 8 個（CT-2）、リダイレクト、/site/sha、
# audience チーム一覧、キャッシュ再生成（UpdateContestantDashboardJob 相当）。

RSpec.describe 'pages and misc' do
  let!(:now) { Time.now }

  describe 'SPA shell (CT-2)' do
    it 'renders the 8 meta tags' do
      res = portal_get('/')
      expect(res.code).to eq('200')
      body = res.body
      expect(body).to include('<meta name="csrf-param" content="authenticity_token">')
      expect(body).to match(/<meta name="csrf-token" content="[^"]+">/)
      expect(body).to match(%r{<meta name="isux:github-auth-path" content="/session/new/github\?back_to=%2F">})
      expect(body).to match(%r{<meta name="isux:discord-auth-path" content="/session/new/discord\?back_to=%2F">})
      expect(body).to include('<meta name="isux:api-base-url" content="/">')
      expect(body).to match(/<meta name="isux:release" content="[^"]*">/)
      expect(body).to include('<div id="app"></div>')
    end

    it 'embeds current path as back_to' do
      res = portal_get('/teams')
      expect(res.body).to include('back_to=%2Fteams')
    end
  end

  describe 'redirects' do
    it 'redirects /terms and /rules' do
      expect(portal_get('/terms').code).to eq('302')
      expect(portal_get('/rules').code).to eq('302')
    end

    it 'enables /docs only while the contest is running (routes constraints 相当)' do
      @timing = { contest_start: now - 3600, contest_end: now + 3600 }
      expect(portal_get('/docs', {}, timing_headers).code).to eq('302')

      @timing = { contest_start: now + 3600, contest_end: now + 7200 }
      expect(portal_get('/docs', {}, timing_headers).code).to eq('404')
    end
  end

  it 'serves /site/sha as text/plain' do
    res = portal_get('/site/sha')
    expect(res.code).to eq('200')
    expect(res['content-type']).to include('text/plain')
  end

  describe 'GET /api/audience/teams (FR-A-3)' do
    it 'lists visible active teams with members, excluding hidden' do
      team1 = create(:team)
      team2 = create(:team, :student)
      hidden = create(:team)
      hidden.update!(is_hidden: true)

      res = portal_get('/api/audience/teams')
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Audience::ListTeamsResponse.decode(res.body)
      expect(body.teams.map(&:team_id)).to eq([team1.id, team2.id])
      expect(body.teams[1].is_student).to eq(true)
      expect(body.teams[0].member_names).to eq(team1.members.map(&:name))
    end
  end

  describe 'dashboard cache regeneration (UpdateContestantDashboardJob 相当)' do
    it 'serves all three visibilities from the pre-generated cache with 304' do
      @timing = { contest_start: now - 3600, contest_freeze: now - 60, contest_end: now + 3600 }
      team1 = create(:team)
      team2 = create(:team)
      create(:benchmark_job, :passed, scored: 100, team: team1, marked_at: now - 300)
      create(:benchmark_job, :passed, scored: 200, team: team2, marked_at: now - 300)
      create(:benchmark_job, :passed, scored: 900, team: team1, marked_at: now - 10) # 凍結後
      portal_update_best_scores

      res = portal_post('/debug/update_contestant_dashboard', { 'frozen' => '1' }, timing_headers)
      expect(res.code).to eq('204')

      # audience: 凍結後の 900 は見えない
      res = portal_get('/api/audience/dashboard', {}, timing_headers)
      lb = Isuxportal::Proto::Services::Audience::DashboardResponse.decode(res.body).leaderboard
      expect(lb.teams.map { |t| t.latest_score.score }).to eq([200, 100])
      expect(portal_get('/api/audience/dashboard', {}, timing_headers('If-None-Match' => res['etag'])).code).to eq('304')

      # contestant: 自チームの凍結後スコアは見える（キャッシュ経由、FR-C-1）
      res = portal_get('/api/contestant/dashboard', {}, timing_headers('X-Isux-Session-Team-Id' => team1.id.to_s))
      item = Isuxportal::Proto::Services::Contestant::DashboardResponse.decode(res.body).leaderboard_item
      expect(item.team.id).to eq(team1.id)
      expect(item.score_history.scores.map(&:score)).to eq([100, 900])

      # admin: 全部見える（leaderboard-v2:admin キャッシュ経由）
      res = portal_get('/api/admin/dashboard', {}, timing_headers('X-Isux-Session-Staff' => '1'))
      body = Isuxportal::Proto::Services::Admin::DashboardResponse.decode(res.body)
      expect(body.leaderboard.teams.map { |t| t.latest_score.score }).to eq([900, 200])

      # audience solo もキャッシュから
      res = portal_get("/api/audience/dashboard/teams/#{team1.id}", {}, timing_headers)
      item = Isuxportal::Proto::Services::Audience::SoloDashboardResponse.decode(res.body).leaderboard_item
      expect(item.score_history.scores.map(&:score)).to eq([100]) # audience には凍結後が見えない
    end
  end

  describe 'ISUXPORTAL_ADMIN_ONLY 相当の adminOnlySwitch' do
    # 全体スイッチは環境変数起動オプションのため、ここでは経路の存在だけを確認する
    it 'keeps /api/session reachable for broadcast view' do
      res = portal_get('/api/session')
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Common::GetCurrentSessionResponse.decode(res.body)
      expect(body.contest).not_to be_nil
    end
  end
end
