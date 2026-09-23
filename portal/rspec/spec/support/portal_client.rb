# Go ポータルへの HTTP クライアント。
# - 競技時刻は @timing の値をテストモードヘッダ X-Isux-Timing-* で渡す
# - セッション Cookie を保持し、リダイレクトを追跡できる（OAuth フロー等）
# - CSRF トークンは SPA シェルの meta タグから取得する（CT-2 の検証を兼ねる）

require 'openssl'
require 'json'

module PortalClient
  TARGET_BASE = ENV.fetch('TARGET_BASE', 'http://127.0.0.1:8081')

  def cookies
    @cookies ||= {}
  end

  def apply_cookies(req)
    return if cookies.empty?
    req['Cookie'] = cookies.map { |k, v| "#{k}=#{v}" }.join('; ')
  end

  def store_cookies(res)
    res.get_fields('Set-Cookie')&.each do |line|
      pair = line.split(';', 2).first
      k, v = pair.split('=', 2)
      cookies[k] = v
    end
  end

  def portal_request(method, path, params: {}, headers: {}, body: nil, form: nil)
    uri = URI(path.start_with?('http') ? path : "#{TARGET_BASE}#{path}")
    uri.query = URI.encode_www_form(params) unless params.empty?
    req = Net::HTTP.const_get(method).new(uri)
    headers.each { |k, v| req[k] = v }
    apply_cookies(req)
    if form
      req.set_form_data(form)
    elsif body
      req.body = body
      req['Content-Type'] ||= 'application/protobuf'
    end
    res = Net::HTTP.start(uri.hostname, uri.port) { |http| http.request(req) }
    store_cookies(res)
    res
  end

  def portal_get(path, params = {}, headers = {})
    portal_request('Get', path, params: params, headers: headers)
  end

  def portal_post(path, params = {}, headers = {}, body: nil, form: nil)
    portal_request('Post', path, params: params, headers: headers, body: body, form: form)
  end

  def portal_put(path, headers = {}, body: nil)
    portal_request('Put', path, headers: headers, body: body)
  end

  def portal_delete(path, headers = {}, body: nil)
    portal_request('Delete', path, headers: headers, body: body)
  end

  # follow_redirects はリダイレクトを最大 limit 回追跡し、最後のレスポンスを返す。
  def follow_redirects(res, headers = {}, limit: 10)
    while res.is_a?(Net::HTTPRedirection) && limit > 0
      location = res['location']
      res = portal_get(location, {}, headers)
      limit -= 1
    end
    res
  end

  # @timing = { contest_start:, contest_freeze:, contest_end:, registration_open:, ... }
  def timing_headers(extra = {})
    h = {}
    (@timing || {}).each do |key, time|
      next if time.nil?
      name = "X-Isux-Timing-#{key.to_s.split('_').map(&:capitalize).join('-')}"
      h[name] = time.utc.iso8601(9)
    end
    h.merge(extra)
  end

  # fetch_csrf_token は SPA シェルの meta タグ（csrf-token）を読む。
  def fetch_csrf_token
    res = portal_get('/', {}, timing_headers)
    raise "GET / failed: #{res.code}" unless res.code == '200'
    m = res.body.match(/<meta name="csrf-token" content="([^"]+)">/)
    raise 'csrf-token meta not found' unless m
    m[1]
  end

  # Contest.leaderboard(...) 相当。/debug/leaderboard は production と同じ
  # leaderboard エンジンをキーワード引数どおりに呼ぶ入口（テストモード限定）。
  def portal_leaderboard(admin: false, team: nil, audience: false, solo: false, solo_item: false, history: true, now: nil)
    params = { 'history' => history ? '1' : '0' }
    params['admin'] = '1' if admin
    params['audience'] = '1' if audience
    params['team_id'] = team.id.to_s if team
    params['solo'] = '1' if solo
    params['solo_item'] = '1' if solo_item
    params['now'] = now.utc.iso8601(9) if now

    res = portal_get('/debug/leaderboard', params, timing_headers)
    raise "GET /debug/leaderboard failed: #{res.code}" unless res.code == '200'

    if solo && solo_item
      Isuxportal::Proto::Resources::LeaderboardItem.decode(res.body)
    else
      Isuxportal::Proto::Resources::Leaderboard.decode(res.body)
    end
  end

  # MaintainBestBenchmarkResultOfTeamJob.perform_now を全チームに適用する相当。
  def portal_update_best_scores
    res = portal_post('/debug/update_best_scores', {}, timing_headers)
    raise "POST /debug/update_best_scores failed: #{res.code}" unless res.code == '204'
  end

  def portal_flushdb
    res = portal_post('/debug/flushdb')
    raise "POST /debug/flushdb failed: #{res.code}" unless res.code == '204'
  end

  # fake OAuth プロバイダにユーザーを登録し、/session/new/{provider} からの
  # リダイレクトを追跡してログイン済みセッションを作る。
  def oauth_login(provider, user)
    res = portal_post("/debug/oauth/#{provider}/register", {}, {}, body: JSON.dump(user))
    raise "oauth register failed: #{res.code}" unless res.code == '200'

    res = portal_get("/session/new/#{provider}", {}, timing_headers)
    raise "session/new failed: #{res.code}" unless res.code == '302'
    follow_redirects(res, timing_headers)
  end

  # BypassToken.create 相当（Ruby 実装との相互運用性の検証を兼ねる）。
  BYPASS_SECRET = "5AQQyz/7oFnJYrxnq/HiLKYmh7QrdIrpCDpKlfvkYAg=".unpack1('m0') # gitleaks:allow isucon14 由来のダミー値

  def create_bypass_token(expiry:, usages:)
    payload = Isuxportal::Proto::Misc::BypassTokenPayload.new(
      filler: [SecureRandom.bytes(6)].pack('m0'),
      expiry: expiry.to_i,
      usages: usages,
    )
    payload_bin = Isuxportal::Proto::Misc::BypassTokenPayload.encode(payload)
    signature = OpenSSL::HMAC.digest('sha384', BYPASS_SECRET, payload_bin)
    "#{[signature].pack('m0')}:#{[payload_bin].pack('m0')}".tr('+/', '-_').gsub(/=/, '')
  end
end
