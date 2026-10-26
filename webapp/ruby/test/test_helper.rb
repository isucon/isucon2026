# frozen_string_literal: true

require 'json'
require 'minitest/autorun'
require 'pg'
require 'pure_jpeg'
require 'rack/test'
require 'stringio'

require_relative '../lib/isureal'

module TestHelper
  SCHEMA = File.read(File.expand_path('../../sql/001-schema.sql', __dir__))

  module_function

  def database_enabled? = ENV['ISUREAL_TEST_DATABASE'] == '1'

  def config(region: 'asia')
    Isureal::Config.from_env.tap { |c| c.region = region }
  end

  # 001-schema.sql を流してテーブルを作り直す。
  def reset_database
    conn = PG.connect(**config.database_options)
    conn.exec('SET client_min_messages TO warning')
    conn.exec(SCHEMA)
    conn
  end

  def jpeg(width, height)
    source = PureJPEG::Source::RawSource.new(width, height) { |x, y| [(x * 7) % 256, (y * 3) % 256, (x ^ y) % 256] }
    PureJPEG.encode(source).to_bytes
  end
end

# DB を使うテストの共通部分。ISUREAL_TEST_DATABASE=1 のときだけ動かす。
class IntegrationTest < Minitest::Test
  include Rack::Test::Methods

  def setup
    skip 'ISUREAL_TEST_DATABASE is not 1' unless TestHelper.database_enabled?

    @conn = TestHelper.reset_database
    @config = TestHelper.config
    @log = StringIO.new
    @app = Isureal.new_server(@config, pool_size: 4, output: @log)
  end

  def teardown
    @conn&.close
  end

  attr_reader :app

  def request_json(method, path, body = nil, token: nil, headers: {})
    env = headers.dup
    env['HTTP_AUTHORIZATION'] = "Bearer #{token}" if token
    if body
      env['CONTENT_TYPE'] ||= 'application/json'
      env[:input] = body.is_a?(String) ? body : JSON.generate(body)
    end
    custom_request(method, path, {}, env)
    last_response
  end

  def body = JSON.parse(last_response.body)

  def assert_status(status, response = last_response)
    assert_equal status, response.status, response.body
  end

  def register(username, region: 'asia', display_name: username)
    request_json('POST', '/api/auth/register', { username:, displayName: display_name, region: })
    assert_status 201
    { id: body['user']['id'], username:, secret_key: body['secretKey'] }
  end

  def login(user)
    request_json('POST', '/api/auth/login', { username: user[:username], secretKey: user[:secret_key] })
    assert_status 200
    body['token']
  end

  def register_and_login(username, region: 'asia')
    user = register(username, region:)
    user.merge(token: login(user))
  end

  def create_admin_token
    @conn.exec_params("INSERT INTO administrators (id, username, password_hash) VALUES ('admin', 'admin', $1)",
                      [Digest::SHA256.hexdigest('password')])
    request_json('POST', '/api/admin/auth/login', { username: 'admin', password: 'password' })
    assert_status 200
    body['token']
  end

  def open_window(admin_token, local_date, region: 'asia')
    request_json('POST', '/api/admin/posting-window', { region:, localDate: local_date }, token: admin_token)
    assert_status 201
    body
  end

  def create_post(user, visibility: 'public', caption: 'hello', image: TestHelper.jpeg(8, 8), fields: {})
    params = { 'image' => Rack::Test::UploadedFile.new(StringIO.new(image), 'image/jpeg', original_filename: 'post.jpg'),
               'caption' => caption, 'visibility' => visibility, 'location_kind' => 'none' }.merge(fields)
    header 'Authorization', "Bearer #{user[:token]}"
    post '/api/posts', params
    header 'Authorization', nil
    last_response
  end

  def befriend(a, b)
    request_json('POST', '/api/friend-requests', { recipientId: b[:id] }, token: a[:token])
    assert_status 201
    request_json('POST', "/api/friend-requests/#{body['relationId']}/accept", token: b[:token])
    assert_status 200
  end
end
