# frozen_string_literal: true

require_relative 'test_helper'

class BindTest < Minitest::Test
  def test_parse_query_matches_go_url_parse_query
    values, error = Isureal::Bind.parse_query('a=1&a=2&b=x+y%21&c&=skip&&d=%zz&e=ok')
    assert_equal({ 'a' => %w[1 2], 'b' => ['x y!'], 'c' => [''], '' => ['skip'], 'e' => ['ok'] }, values)
    assert_equal 'invalid URL escape "%zz"', error

    _, error = Isureal::Bind.parse_query('a=1;b=2')
    assert_equal 'invalid semicolon separator in query', error
  end

  def test_parse_time_accepts_rfc3339_only
    assert_equal Time.utc(2026, 10, 26, 1, 2, 3), Isureal::Bind.parse_time('2026-10-26T01:02:03Z')
    assert_equal Time.utc(2026, 10, 25, 16, 2, 3, Rational(123_456_789, 1000)),
                 Isureal::Bind.parse_time('2026-10-26T01:02:03.1234567891+09:00')
    %w[2026-10-26 2026-10-26T01:02:03 2026-02-30T00:00:00Z 2026-10-26T24:00:00Z 2026-10-26t01:02:03Z].each do |value|
      assert_raises(ArgumentError, value) { Isureal::Bind.parse_time(value) }
    end
  end

  def test_parse_float_matches_strconv
    assert_in_delta 0.0, Isureal::Bind.parse_float('')
    assert_in_delta 35.5, Isureal::Bind.parse_float('35.5')
    assert_in_delta 0.5, Isureal::Bind.parse_float('.5')
    assert_in_delta 1.0, Isureal::Bind.parse_float('1.')
    assert_in_delta(-1e3, Isureal::Bind.parse_float('-1e3'))
    assert_predicate Isureal::Bind.parse_float('NaN'), :nan?
    assert_equal(-Float::INFINITY, Isureal::Bind.parse_float('-Inf'))
    ['1_0', ' 1', '0x10', 'abc', '1e400'].each do |value|
      assert_raises(ArgumentError, value) { Isureal::Bind.parse_float(value) }
    end
  end

  def test_parse_int_matches_strconv
    assert_equal 0, Isureal::Bind.parse_int('')
    assert_equal 7, Isureal::Bind.parse_int('+007')
    assert_equal(-12, Isureal::Bind.parse_int('-12'))
    assert_equal((2**63) - 1, Isureal::Bind.parse_int('9223372036854775807'))
    assert_equal(-(2**63), Isureal::Bind.parse_int('-9223372036854775808'))
    ['1_0', ' 1', '0x10', '1.0', 'abc', '9223372036854775808'].each do |value|
      assert_raises(ArgumentError, value) { Isureal::Bind.parse_int(value) }
    end
  end
end

class ResponseTest < Minitest::Test
  def test_encode_json_escapes_like_go
    assert_equal %({"a":"\\u003cb\\u003e\\u0026\\u0008\\u000c\\\\b\\u2028\\u2029","n":1}\n),
                 Isureal::Response.encode_json({ a: "<b>&\b\f\\b\u2028\u2029", n: 1 })
  end

  def test_format_time_is_rfc3339_nano_in_utc
    assert_equal '2026-10-26T01:02:03Z', Isureal::Response.format_time(Time.utc(2026, 10, 26, 1, 2, 3))
    assert_equal '2026-10-25T16:02:03.12Z', Isureal::Response.format_time(Time.new(2026, 10, 26, 1, 2, 3.12r, '+09:00'))
  end
end

class ImageSignatureTest < Minitest::Test
  def test_signature_vector_matches_go
    key = Digest::SHA256.digest('test-secret')
    signature = Isureal::ImageSignature.signature(key, '550e8400-e29b-41d4-a716-446655440000', 1_790_553_600)
    assert_equal 'qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84', Isureal::ImageSignature.encode(signature)
    assert_equal key, Isureal::ImageSignature.signing_key(Digest::SHA256.hexdigest('test-secret'))
    ['', 'hash', 'abcd', nil].each do |invalid|
      assert_raises(ArgumentError) { Isureal::ImageSignature.signing_key(invalid) }
    end
  end

  def test_signed_url_lifetime
    key = Digest::SHA256.digest('test-secret')
    user = Isureal::User.new(id: 'owner', image_key: key)
    before = Time.now.to_i
    url = Isureal::ImageSignature.signed_url(user, 'image-id')
    after = Time.now.to_i
    match = %r{\A/api/images/image-id\?exp=(\d+)&sig=([A-Za-z0-9_-]{43})\z}.match(url)
    refute_nil match, url
    assert_includes (before + 86_400)..(after + 86_400), match[1].to_i
    assert_equal Isureal::ImageSignature.encode(Isureal::ImageSignature.signature(key, 'image-id', match[1].to_i)), match[2]
  end

  def test_decode_rejects_non_canonical_signatures
    signature = Isureal::ImageSignature.signature(Digest::SHA256.digest('k'), 'id', 1)
    encoded = Isureal::ImageSignature.encode(signature)
    assert_equal signature, Isureal::ImageSignature.decode(encoded)
    assert_nil Isureal::ImageSignature.decode("#{encoded}=")
    assert_nil Isureal::ImageSignature.decode("+#{encoded[1..]}")
    assert_nil Isureal::ImageSignature.decode("#{encoded[0..-2]}B")
    assert_nil Isureal::ImageSignature.decode(encoded[0..-2])
  end
end

class JPEGTest < Minitest::Test
  def test_decode_validates_whole_image
    data = TestHelper.jpeg(20, 10)
    image = Isureal::JPEG.decode(data)
    assert_equal [20, 10], [image.width, image.height]
    assert_nil Isureal::JPEG.decode(data[0, data.bytesize * 2 / 3])
    assert_nil Isureal::JPEG.decode("\x89PNG\r\n\x1a\n".b)
    assert_nil Isureal::JPEG.decode(''.b)
  end
end

class ActionLogTest < Minitest::Test
  def app_for(min_level)
    output = StringIO.new
    inner = lambda do |env|
      case [env['REQUEST_METHOD'], env['PATH_INFO']]
      in ['POST', '/api/auth/login']
        env['isureal.username'] = JSON.parse(env['rack.input'].read)['username']
        [401, {}, []]
      in ['POST', '/api/posts']
        env['isureal.session'] = Isureal::Session.new(user_id: 'user-1')
        [201, {}, []]
      in ['GET', '/api/feeds/friends']
        env['isureal.session'] = Isureal::Session.new(user_id: 'user-1')
        [200, {}, []]
      in ['GET', '/api/users/someone']
        [500, {}, []]
      in ['PUT', '/api/me/profile-image']
        env['isureal.abuse'] = 'invalid_image'
        [400, {}, []]
      in ['GET', '/api/images/image-id']
        env['isureal.abuse'] = 'invalid_signature'
        [403, {}, []]
      else
        [404, {}, []]
      end
    end
    [Isureal::ActionLog.new(inner, min_level:, output:), output]
  end

  def send_request(app, method, path, body = '')
    app.call(Rack::MockRequest.env_for(path, method:, input: body, 'HTTP_X_CLIENT_IP' => '203.0.113.7'))
  end

  def lines(output) = output.string.lines.map { |line| JSON.parse(line) }

  def test_writes_one_json_line_per_request
    app, output = app_for('debug')
    send_request(app, 'POST', '/api/auth/login', '{"username":"alice","secretKey":"wrong"}')
    send_request(app, 'POST', '/api/auth/login', '{"username":"Not Valid","secretKey":"wrong"}')
    send_request(app, 'POST', '/api/posts', '{}')
    send_request(app, 'GET', '/api/feeds/friends')
    send_request(app, 'GET', '/api/users/someone')
    send_request(app, 'GET', '/api/unknown')
    assert_equal [
      { 'action' => 'login', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 401, 'user_id' => nil, 'username' => 'alice' },
      { 'action' => 'login', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 401, 'user_id' => nil, 'username' => nil },
      { 'action' => 'create_post', 'ip' => '203.0.113.7', 'level' => 'info', 'status' => 201, 'user_id' => 'user-1' },
      { 'action' => 'get_feed', 'ip' => '203.0.113.7', 'level' => 'debug', 'status' => 200, 'user_id' => 'user-1' },
      { 'action' => 'get_user', 'ip' => '203.0.113.7', 'level' => 'error', 'status' => 500, 'user_id' => nil },
      { 'action' => nil, 'ip' => '203.0.113.7', 'level' => 'debug', 'status' => 404, 'user_id' => nil },
    ], lines(output)
  end

  def test_info_level_skips_reads
    app, output = app_for('info')
    send_request(app, 'GET', '/api/feeds/friends')
    send_request(app, 'POST', '/api/posts', '{}')
    send_request(app, 'PUT', '/api/me/profile-image')
    send_request(app, 'GET', '/api/images/image-id')
    assert_equal [['create_post', nil], ['put_profile_image', 'invalid_image'], ['get_image', 'invalid_signature']],
                 lines(output).map { |entry| [entry['action'], entry['abuse']] }
  end
end
