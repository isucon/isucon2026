# 認証（OAuth）と参加登録フローの受け入れテスト（FR-AUTH / FR-REG）。
# fake OAuth プロバイダ（テストモード限定）で実フローを cookie 込みで通す。

RSpec.describe 'registration flow' do
  def github_user(n)
    { 'id' => 1000 + n, 'login' => "gh-user-#{n}", 'avatar_url' => "https://avatars.test.invalid/#{n}" }
  end

  def discord_user(n, discriminator: '0')
    { 'id' => "dc-#{n}", 'username' => "dc-user-#{n}", 'discriminator' => discriminator, 'avatar' => 'abc' }
  end

  def registration_session(params = {})
    res = portal_get('/api/registration/session', params, timing_headers)
    expect(res.code).to eq('200')
    Isuxportal::Proto::Services::Registration::GetRegistrationSessionResponse.decode(res.body)
  end

  def create_team!(team_name: 'flow-team', name: 'Alice', student: false)
    csrf = fetch_csrf_token
    body = Isuxportal::Proto::Services::Registration::CreateTeamRequest.encode(
      Isuxportal::Proto::Services::Registration::CreateTeamRequest.new(
        team_name: team_name, name: name, email_address: 'flow@test.invalid', is_student: student,
      )
    )
    portal_post('/api/registration/team', {}, timing_headers('X-Csrf-Token' => csrf), body: body)
  end

  it 'walks NOT_LOGGED_IN → CREATABLE → JOINED → invite → withdraw' do
    # 未ログイン
    expect(registration_session.status).to eq(:NOT_LOGGED_IN)

    # GitHub ログインのみでは NOT_LOGGED_IN のまま
    res = oauth_login('github', github_user(1))
    expect(res.code).to eq('200') # /registration の SPA シェル
    expect(registration_session.status).to eq(:NOT_LOGGED_IN)

    # Discord もログインすると CREATABLE
    oauth_login('discord', discord_user(1))
    session = registration_session
    expect(session.status).to eq(:CREATABLE)
    expect(session.github_login).to eq('gh-user-1')
    expect(session.discord_tag).to eq('dc-user-1') # discriminator '0' は username のみ（FR-AUTH-2）

    # チーム作成（CSRF トークン必須、CT-2）
    res = create_team!
    expect(res.code).to eq('200')
    team_id = Isuxportal::Proto::Services::Registration::CreateTeamResponse.decode(res.body).team_id
    expect(team_id).to be > 0

    team = Team.find(team_id)
    zones = %w[apne1-az1 apne1-az2 apne1-az4]
    expect(team.availability_zone).to eq(zones[team_id % 3]) # FR-REG-3
    expect(team.leader_id).to eq(Contestant.find_by!(github_id: '1001').id)
    expect(team.final_participation).to eq(true) # 単一開催: 作成時 true（計画書 §11.4）

    session = registration_session
    expect(session.status).to eq(:JOINED)
    expect(session.team.detail.invite_token).to eq(team.invite_token) # 代表者には detail が見える
    invite_url = session.member_invite_url
    expect(invite_url).to include("team_id=#{team_id}")

    # GET /api/session に契約どおりの情報が載る
    res = portal_get('/api/session', {}, timing_headers)
    me = Isuxportal::Proto::Services::Common::GetCurrentSessionResponse.decode(res.body)
    expect(me.contestant.name).to eq('Alice')
    expect(me.team.id).to eq(team_id)

    # 2 人目が招待 URL から参加（FR-REG-4）
    @cookies = {}
    oauth_login('github', github_user(2))
    oauth_login('discord', discord_user(2, discriminator: '1234'))
    session = registration_session('team_id' => team_id.to_s, 'invite_token' => team.invite_token)
    expect(session.status).to eq(:JOINABLE)
    expect(session.discord_tag).to eq('dc-user-2#1234')

    csrf = fetch_csrf_token
    body = Isuxportal::Proto::Services::Registration::JoinTeamRequest.encode(
      Isuxportal::Proto::Services::Registration::JoinTeamRequest.new(
        team_id: team_id, invite_token: team.invite_token, name: 'Bob', is_student: true,
      )
    )
    res = portal_post('/api/registration/contestant', {}, timing_headers('X-Csrf-Token' => csrf), body: body)
    expect(res.code).to eq('200')
    expect(Contestant.where(team_id: team_id).count).to eq(2)
    expect(team.reload.student).to eq(false) # 全員学生ではない（DM-5）

    # メンバー辞退（FR-REG-6）: Bob 自身が抜ける
    csrf = fetch_csrf_token
    res = portal_delete('/api/registration', timing_headers('X-Csrf-Token' => csrf))
    expect(res.code).to eq('200')
    expect(Contestant.where(team_id: team_id).count).to eq(1)
    expect(team.reload.withdrawn).to eq(false)
  end

  it 'rejects invalid invite URL with 404 + 招待URLが無効です' do
    res = portal_get('/api/registration/session', { 'team_id' => '9999', 'invite_token' => 'bad' }, timing_headers)
    expect(res.code).to eq('404')
    err = Isuxportal::Proto::Error.decode(res.body)
    expect(err.human_descriptions).to include('招待URLが無効です')
  end

  it 'closes registration outside the window unless bypassed (FR-REG-2 / FR-AUTH-7)' do
    now = Time.now
    @timing = { registration_open: now - 7200, registration_close: now - 3600 }

    oauth_login('github', github_user(3))
    oauth_login('discord', discord_user(3))
    expect(registration_session.status).to eq(:CLOSED)
    res = create_team!(team_name: 'closed-team', name: 'Carol')
    expect(res.code).to eq('422')
    err = Isuxportal::Proto::Error.decode(res.body)
    expect(err.name).to eq('Contest::RegistrationClosed')

    # bypass token（CREATE_TEAM）で通る。Ruby 側で生成したトークンを Go が検証する（FR-AUTH-7）
    token = create_bypass_token(expiry: now + 3600, usages: [:CREATE_TEAM])
    res = portal_get("/session/bypass/#{token}", {}, timing_headers)
    expect(res.code).to eq('302')

    expect(registration_session.status).to eq(:CREATABLE)
    res = create_team!(team_name: 'bypassed-team', name: 'Carol')
    expect(res.code).to eq('200')
  end

  it 'rejects expired bypass token with 404 no' do
    token = create_bypass_token(expiry: Time.now - 10, usages: [:CREATE_TEAM])
    res = portal_get("/session/bypass/#{token}")
    expect(res.code).to eq('404')
    expect(res.body).to eq('no')
  end

  it 'blocks Discordbot/Slackbot UA on /session/new/* (FR-AUTH-4)' do
    res = portal_get('/session/new/github', {}, 'User-Agent' => 'Mozilla/5.0 (compatible; Discordbot/2.0)')
    expect(res.code).to eq('404')
    expect(res.body).to eq('Beep beep, boop beep?')

    res = portal_get('/session/new/github', { 'beepbeep' => '1' }, 'User-Agent' => 'Slackbot-LinkExpanding')
    expect(res.code).to eq('302')
  end

  it 'requires CSRF token for mutating API with real session (CT-2)' do
    oauth_login('github', github_user(4))
    oauth_login('discord', discord_user(4))
    body = Isuxportal::Proto::Services::Registration::CreateTeamRequest.encode(
      Isuxportal::Proto::Services::Registration::CreateTeamRequest.new(
        team_name: 'csrf-team', name: 'Dave', email_address: 'x@test.invalid',
      )
    )
    res = portal_post('/api/registration/team', {}, timing_headers, body: body)
    expect(res.code).to eq('422')
    err = Isuxportal::Proto::Error.decode(res.body)
    expect(err.name).to eq('ActionController::InvalidAuthenticityToken')
  end

  it 'logs out with DELETE /session (FR-AUTH-6)' do
    oauth_login('github', github_user(5))
    oauth_login('discord', discord_user(5))
    csrf = fetch_csrf_token
    res = portal_delete('/session', timing_headers('X-Csrf-Token' => csrf))
    expect(res.code).to eq('302')
    expect(registration_session.status).to eq(:NOT_LOGGED_IN)
  end

  it 'switches discord account for logged-in contestant (FR-AUTH-3)' do
    oauth_login('github', github_user(6))
    oauth_login('discord', discord_user(6))
    create_team!(team_name: 'switch-team', name: 'Eve')
    contestant = Contestant.find_by!(github_id: '1006')
    expect(contestant.discord_id).to eq('dc-6')

    # 別の Discord アカウントで callback → 差し替え
    oauth_login('discord', discord_user(60))
    expect(contestant.reload.discord_id).to eq('dc-60')
  end
end
