# envcheck 連携（FR-E）の受け入れテスト。非 protobuf 3 本（JSON / text/plain / 空ボディ）と
# 認証 2 方式（CheckerToken / HMAC-SHA384 署名）、インスタンス登録の副作用を検証する。

require 'openssl'

RSpec.describe 'envcheck API' do
  let!(:now) { Time.now }
  let!(:team1) { create(:team) }

  before do
    @timing = { contest_start: now - 3600, contest_freeze: now + 3600, contest_end: now + 7200 }
  end

  CHECKER_SECRET = '2RHAlLoifERG1EEc/5dwyzcZGitfUn1k9YkHTwk7HiRO'.unpack1('m*')

  # CheckerToken.create 相当（Ruby 生成 → Go 検証の相互運用チェック）
  def checker_token(team_id:, expiry: Time.now.to_i + 3600)
    payload = JSON.dump(team_id: team_id, expiry: expiry)
    signature = OpenSSL::HMAC.digest('sha384', CHECKER_SECRET, payload)
    "#{[signature].pack('m0')}:#{[payload].pack('m0')}".tr('+/', '-_').gsub(/=/, '')
  end

  def hmac_signature(team_id)
    OpenSSL::HMAC.hexdigest('sha384', 'himitsudayo', team_id.to_s)
  end

  describe 'GET /api/env_check_info（FR-E-1: JSON）' do
    it 'returns ami/az info with checker token' do
      res = portal_get('/api/env_check_info', { 'name' => 'test-boot', 'token' => checker_token(team_id: team1.id) }, timing_headers)
      expect(res.code).to eq('200')
      expect(res['content-type']).to include('application/json')
      body = JSON.parse(res.body)
      expect(body['ami_id']).to eq('ami-test')
      expect(body['ami_ids']).to eq(['ami-test'])
      expect(body['az_id']).to eq(team1.availability_zone)
    end

    it 'accepts legacy HMAC signature auth (FR-E-4)' do
      res = portal_get('/api/env_check_info', {
        'name' => 'contest', 'team_id' => team1.id.to_s, 'signature' => hmac_signature(team1.id),
      }, timing_headers)
      expect(res.code).to eq('200')
      expect(JSON.parse(res.body)['ami_ids']).to eq(['ami-y'])
    end

    it 'rejects unknown name with 400' do
      res = portal_get('/api/env_check_info', { 'name' => 'nazo', 'token' => checker_token(team_id: team1.id) }, timing_headers)
      expect(res.code).to eq('400')
    end

    it 'rejects invalid token / signature' do
      res = portal_get('/api/env_check_info', { 'name' => 'contest', 'token' => 'bogus' }, timing_headers)
      expect(res.code).to eq('401')

      res = portal_get('/api/env_check_info', { 'name' => 'contest', 'team_id' => team1.id.to_s, 'signature' => 'bad' }, timing_headers)
      expect(res.code).to eq('400')

      res = portal_get('/api/env_check_info', { 'name' => 'contest' }, timing_headers)
      expect(res.code).to eq('400')
    end
  end

  describe 'GET /api/ssh_public_keys（FR-E-2: text/plain）' do
    it 'lists keys as "<key> <contestant_id>@<github_login>" with trailing newline' do
      leader = team1.members.first
      SshPublicKey.create!(contestant_id: leader.id, public_key: 'ssh-ed25519 AAAA1')
      SshPublicKey.create!(contestant_id: leader.id, public_key: 'ssh-ed25519 AAAA2')

      res = portal_get('/api/ssh_public_keys', { 'token' => checker_token(team_id: team1.id) }, timing_headers)
      expect(res.code).to eq('200')
      expect(res['content-type']).to include('text/plain')
      expect(res.body).to eq(
        "ssh-ed25519 AAAA1 #{leader.id}@#{leader.github_login}\n" \
        "ssh-ed25519 AAAA2 #{leader.id}@#{leader.github_login}\n"
      )
    end
  end

  describe 'POST /api/env_checks（FR-E-3/5: 空ボディ + 副作用）' do
    def post_check(name:, ip:, passed: true)
      portal_post('/api/env_checks', {}, timing_headers, form: {
        'token' => checker_token(team_id: team1.id),
        'name' => name,
        'ip_address' => ip,
        'passed' => passed ? 'true' : 'false',
        'message' => 'msg',
        'admin_message' => 'admin msg',
        'raw_data' => '{}',
      })
    end

    it 'saves result with empty body and registers ContestantInstance for contest<N>' do
      res = post_check(name: 'contest1', ip: '203.0.113.10')
      expect(res.code).to eq('204')
      expect(res.body).to be_nil.or eq('')

      check = EnvCheck.find_by!(team_id: team1.id, name: 'contest1')
      expect(check.passed).to eq(true)
      expect(check.admin_message).to eq('admin msg')

      instance = ContestantInstance.find_by!(team_id: team1.id, number: 1)
      expect(instance.cloud_id).to eq("contest-#{team1.id}-1")       # ダミー cloud_id
      expect(instance.private_ipv4_address).to eq('192.168.0.11')    # 捏造 private IP
      expect(instance.public_ipv4_address).to eq('203.0.113.10')
      expect(instance.status).to eq('running')
    end

    it 'does not update instance after contest end (FR-E-5)' do
      @timing = { contest_start: now - 7200, contest_freeze: now - 3660, contest_end: now - 3600 }
      res = post_check(name: 'contest2', ip: '203.0.113.20')
      expect(res.code).to eq('204')
      expect(EnvCheck.where(team_id: team1.id, name: 'contest2').count).to eq(1) # 結果は保存される
      expect(ContestantInstance.find_by(team_id: team1.id, number: 2)).to be_nil # 登録はされない
    end

    it 'skips instance registration for contest-unknown' do
      res = post_check(name: 'contest-unknown', ip: '203.0.113.30')
      expect(res.code).to eq('204')
      expect(ContestantInstance.where(team_id: team1.id).count).to eq(0)
    end
  end

  describe 'GET /api/registration/env_check' do
    it 'returns CFn template with embedded checker token' do
      leader = team1.members.first
      res = portal_get('/api/registration/env_check', {}, timing_headers('X-Isux-Session-Contestant-Id' => leader.id.to_s))
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Registration::GetEnvCheckInformationResponse.decode(res.body)
      expect(body.template).to include('AWSTemplateFormatVersion')
      expect(body.template).to include('ami-test')
      expect(body.template).to include(team1.availability_zone)
      expect(body.status).to eq(:DONE)
    end
  end
end
