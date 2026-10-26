# frozen_string_literal: true

require 'zip'
require_relative 'test_helper'

class UserAPITest < IntegrationTest
  def test_register_login_update_and_logout
    user = register('alice', display_name: 'アリス')
    request_json('POST', '/api/auth/register', { username: 'alice', displayName: 'Other', region: 'asia' })
    assert_status 409
    assert_equal({ 'error' => 'username_taken' }, body)
    request_json('POST', '/api/auth/register', { username: 'Bad Name', displayName: 'x', region: 'asia' })
    assert_equal({ 'error' => 'invalid_registration' }, body)

    request_json('POST', '/api/auth/login', { username: 'alice', secretKey: 'wrong' })
    assert_status 401
    first = login(user)
    second = login(user)
    request_json('GET', '/api/me', token: first)
    assert_status 401
    request_json('GET', '/api/me', token: second)
    assert_status 200
    assert_equal({ 'id' => user[:id], 'username' => 'alice', 'displayName' => 'アリス', 'bio' => '', 'region' => 'asia',
                   'accountType' => 'regular', 'status' => 'active', 'profileImageUrl' => nil }, body)

    request_json('PATCH', '/api/me', {}, token: second)
    assert_equal({ 'error' => 'invalid_profile' }, body)
    request_json('PATCH', '/api/me', { bio: '<b>' }, token: second)
    assert_status 200
    assert_equal '<b>', body['bio']
    refute_includes last_response.body, '<'

    header 'Authorization', "Bearer #{second}"
    header 'Content-Type', 'image/jpeg'
    put '/api/me/profile-image', TestHelper.jpeg(16, 16)
    assert_status 204
    put '/api/me/profile-image', 'not a jpeg'
    assert_equal({ 'error' => 'invalid_profile_image' }, body)
    header 'Content-Type', nil
    header 'Authorization', nil
    request_json('GET', '/api/me', token: second)
    image_url = body['profileImageUrl']
    get image_url
    assert_status 200
    assert_equal 'image/jpeg', last_response.content_type
    assert_equal 'no-store', last_response.headers['cache-control']

    request_json('POST', '/api/auth/logout', token: second)
    assert_status 204
    request_json('GET', '/api/me', token: second)
    assert_status 401
    assert_includes @log.string, '"action":"login","ip":"127.0.0.1","level":"info","status":401,"user_id":null,"username":"alice"'
  end

  def test_region_mismatch_and_unknown_routes
    user = register_and_login('traveler', region: 'europe')
    request_json('GET', '/api/me', token: user[:token])
    assert_status 403
    assert_equal({ 'error' => 'region_mismatch' }, body)
    request_json('PATCH', '/api/me', { region: 'asia' }, token: user[:token])
    assert_status 200
    request_json('GET', '/api/me', token: user[:token])
    assert_status 200

    request_json('GET', '/api/unknown')
    assert_status 404
    assert_equal({ 'error' => 'Not Found' }, body)
    request_json('POST', '/api/me')
    assert_status 405
    assert_equal 'OPTIONS, GET, PATCH', last_response.headers['allow']
    request_json('POST', '/api/auth/login', '{"username":"x"}', headers: { 'CONTENT_TYPE' => 'text/plain' })
    assert_status 415
    assert_equal({ 'error' => 'code=415, message=Unsupported Media Type' }, body)
  end

  def test_ip_ban
    @conn.exec("INSERT INTO ip_bans (ip, banned_until) VALUES ('203.0.113.7', clock_timestamp() + interval '2 minutes')")
    request_json('POST', '/api/auth/login', { username: 'nobody', secretKey: 'x' }, headers: { 'HTTP_X_CLIENT_IP' => '203.0.113.7' })
    assert_status 403
    assert_equal({ 'error' => 'ip_banned' }, body)
    request_json('GET', '/api/images/missing', headers: { 'HTTP_X_CLIENT_IP' => '203.0.113.7' })
    assert_status 403
    assert_equal({ 'error' => 'ip_banned' }, body)
    request_json('POST', '/api/auth/login', { username: 'nobody', secretKey: 'x' }, headers: { 'HTTP_X_CLIENT_IP' => '198.51.100.1' })
    assert_status 401
  end
end

class PostAPITest < IntegrationTest
  def setup
    super
    @admin = create_admin_token
    @alice = register_and_login('alice')
    @bob = register_and_login('bob')
    @carol = register_and_login('carol')
  end

  def test_post_lifecycle_and_slots
    assert_equal 409, create_post(@alice).status
    open_window(@admin, '2026-09-24')
    request_json('GET', '/api/posting-window/current', token: @alice[:token])
    assert_equal [false, true, 0], body.values_at('hasPosted', 'canPost', 'deletionCount')

    assert_equal 400, create_post(@alice, visibility: 'secret').status
    assert_equal 400, create_post(@alice, image: 'not a jpeg').status
    assert_equal 400, create_post(@alice, fields: { 'location_kind' => 'exact', 'latitude' => '91', 'longitude' => '0' }).status
    response = create_post(@alice, fields: { 'location_kind' => 'exact', 'latitude' => '35.5', 'longitude' => '139.25' })
    assert_status 201, response
    created = JSON.parse(response.body)
    assert_equal({ 'kind' => 'exact', 'latitude' => 35.5, 'longitude' => 139.25 }, created['location'])
    assert_equal [true, 0], created.values_at('onTime', 'lateSeconds')
    assert_equal 409, create_post(@alice).status

    request_json('GET', '/api/posts/me/current', token: @alice[:token])
    assert_equal created['id'], body['id']
    request_json('PATCH', "/api/posts/#{created['id']}", { caption: 'edited' }, token: @alice[:token])
    assert_equal 'edited', body['caption']
    request_json('PATCH', "/api/posts/#{created['id']}", { caption: 'x' }, token: @bob[:token])
    assert_equal({ 'error' => 'post_not_editable' }, body)

    get created['imageUrl']
    assert_status 200
    request_json('DELETE', "/api/posts/#{created['id']}", token: @alice[:token])
    assert_status 204
    get created['imageUrl']
    assert_equal({ 'error' => 'image_forbidden' }, body)
    assert_equal 201, create_post(@alice).status
    request_json('DELETE', "/api/posts/#{current_post_id}", token: @alice[:token])
    assert_status 204
    response = create_post(@alice)
    assert_equal({ 'error' => 'post_slot_unavailable' }, JSON.parse(response.body))
    request_json('GET', '/api/posting-window/current', token: @alice[:token])
    assert_equal [false, false, 2], body.values_at('hasPosted', 'canPost', 'deletionCount')
  end

  def test_feed_visibility
    befriend(@alice, @bob)
    befriend(@bob, @carol)
    open_window(@admin, '2026-09-24')
    alice_post = JSON.parse(create_post(@alice, visibility: 'friends_of_friends').body)
    bob_post = JSON.parse(create_post(@bob, visibility: 'friends').body)

    request_json('GET', '/api/feeds/friends', token: @carol[:token])
    assert_equal [], body['items']
    request_json('GET', "/api/posts/#{bob_post['id']}", token: @carol[:token])
    assert_status 404

    create_post(@carol, visibility: 'public')
    request_json('GET', '/api/feeds/friends', token: @carol[:token])
    assert_equal [bob_post['id']], body['items'].map { |item| item['id'] }
    request_json('GET', '/api/feeds/friends-of-friends', token: @carol[:token])
    assert_equal [alice_post['id']], body['items'].map { |item| item['id'] }
    request_json('GET', '/api/feeds/public', token: @bob[:token])
    assert_equal ['carol'], body['items'].map { |item| item['author']['username'] }
    request_json('GET', '/api/feeds/public?beforeId=x', token: @bob[:token])
    assert_equal({ 'error' => 'invalid_pagination' }, body)

    request_json('PUT', "/api/blocks/#{@bob[:id]}", token: @carol[:token])
    assert_status 204
    request_json('GET', '/api/feeds/friends', token: @carol[:token])
    assert_equal [], body['items']
    request_json('GET', '/api/relationships/friends', token: @carol[:token])
    assert_equal [], body['items']
  end

  def test_comments_realmojis_memories_and_export
    befriend(@alice, @bob)
    open_window(@admin, '2026-09-24')
    post_id = JSON.parse(create_post(@alice, visibility: 'friends').body)['id']

    request_json('POST', "/api/posts/#{post_id}/comments", { body: 'hi' }, token: @bob[:token])
    assert_equal({ 'error' => 'interaction_forbidden' }, body)
    create_post(@bob)
    request_json('POST', "/api/posts/#{post_id}/comments", { body: '' }, token: @bob[:token])
    assert_equal({ 'error' => 'invalid_interaction' }, body)
    request_json('POST', "/api/posts/#{post_id}/comments", { body: 'nice' }, token: @bob[:token])
    assert_status 201
    comment = body
    assert_equal 'bob', comment.dig('author', 'username')

    header 'Authorization', "Bearer #{@bob[:token]}"
    image = Rack::Test::UploadedFile.new(StringIO.new(TestHelper.jpeg(4, 4)), 'image/jpeg', original_filename: 'moji.jpg')
    post "/api/posts/#{post_id}/realmojis", 'image' => image, 'reactionType' => 'smile'
    assert_status 201
    reaction = body
    post "/api/posts/#{post_id}/realmojis", 'image' => image, 'reactionType' => 'wink'
    assert_equal({ 'error' => 'invalid_realmoji' }, body)
    header 'Authorization', nil

    request_json('GET', "/api/posts/#{post_id}/realmojis", token: @alice[:token])
    assert_equal [reaction['id']], body['items'].map { |item| item['id'] }
    request_json('GET', "/api/posts/#{post_id}/comments", token: @alice[:token])
    assert_equal [comment['id']], body['items'].map { |item| item['id'] }

    request_json('GET', '/api/memories/2026-09-24', token: @alice[:token])
    assert_equal [post_id], body['items'].map { |item| item['id'] }
    assert_equal [reaction['id']], body['items'][0]['realmojis'].map { |item| item['id'] }
    assert_equal [comment['id']], body['items'][0]['comments'].map { |item| item['id'] }
    request_json('GET', '/api/memories?month=2026-13', token: @alice[:token])
    assert_equal({ 'error' => 'invalid_memory_period' }, body)

    request_json('DELETE', "/api/comments/#{comment['id']}", token: @carol[:token])
    assert_equal({ 'error' => 'interaction_forbidden' }, body)
    request_json('DELETE', "/api/comments/#{comment['id']}", token: @alice[:token])
    assert_status 204
    request_json('DELETE', "/api/realmojis/#{reaction['id']}", token: @bob[:token])
    assert_status 204

    request_json('GET', '/api/me/export', token: @alice[:token])
    assert_status 200
    assert_equal 'application/zip', last_response.content_type
    files = {}
    Zip::InputStream.open(StringIO.new(last_response.body)) do |zip|
      while (entry = zip.get_next_entry)
        files[entry.name] = zip.read
      end
    end
    manifest = JSON.parse(files.fetch('posts.json'))
    assert_equal [post_id], manifest.map { |item| item['id'] }
    assert_equal 'images/000001.jpg', manifest[0]['imagePath']
    assert Isureal::JPEG.decode(files.fetch('images/000001.jpg'))
  end

  def test_follow_requires_certification
    request_json('PUT', "/api/follows/#{@bob[:id]}", token: @alice[:token])
    assert_equal({ 'error' => 'relationship_conflict' }, body)
    request_json('PUT', "/api/admin/users/#{@bob[:id]}/certification", token: @admin)
    assert_status 204
    request_json('DELETE', "/api/admin/users/#{@bob[:id]}/certification", token: @admin)
    assert_equal({ 'error' => 'certification_irreversible' }, body)
    request_json('PUT', "/api/follows/#{@bob[:id]}", token: @alice[:token])
    assert_status 204
    request_json('GET', '/api/relationships/followers', token: @bob[:token])
    assert_equal ['alice'], body['items'].map { |item| item.dig('user', 'username') }
    request_json('GET', '/api/users?q=BOB', token: @alice[:token])
    assert_equal ['bob'], body['items'].map { |item| item['username'] }
  end

  private

  def current_post_id
    request_json('GET', '/api/posts/me/current', token: @alice[:token])
    body['id']
  end
end
