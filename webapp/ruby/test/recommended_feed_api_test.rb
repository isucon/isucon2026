# frozen_string_literal: true

require 'securerandom'
require_relative 'test_helper'

class RecommendedFeedAPITest < IntegrationTest
  FEED = '/api/feeds/friends-of-friends'

  def test_ranking_and_pagination
    %w[viewer f1 f2 a1 a2 x].each do |id|
      @conn.exec_params("INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $1, $2, $3, 'asia')",
                        [id, Digest::SHA256.hexdigest('secret'), id])
    end
    token = login({ username: 'viewer', secret_key: 'secret' })
    @conn.exec(<<~SQL)
      INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at)
      VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)
    SQL
    %w[viewer a1 a2 x].each_with_index do |author, i|
      @conn.exec_params("INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, $3, 'image/jpeg')",
                        ["#{author}-image", author, { value: 'image', format: 1 }])
      @conn.exec_params(<<~SQL, [author, "#{author}-image", "2026-09-23T00:01:0#{i}Z"])
        INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at)
        VALUES ($1, $1, 'w', $2, 'caption', 'friends_of_friends', 'none', $3)
      SQL
    end
    [%w[f1 viewer], %w[f2 viewer], %w[a1 f1], %w[a1 f2], %w[a2 f1], %w[a1 x], %w[a2 x], %w[f2 x]].each do |pair|
      @conn.exec_params('INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, $2, $3)', [SecureRandom.uuid, *pair.minmax])
    end

    page = assert_ranking(token, FEED, 'a1:7', 'x:6', 'a2:4')
    @conn.exec_params("INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a2', 'f1', 'rm', 'smile', clock_timestamp())",
                      [SecureRandom.uuid])
    @conn.exec_params("INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES ($1, 'a2', 'f1', 'nice', clock_timestamp()), ($2, 'a2', 'x', 'nice', clock_timestamp())",
                      [SecureRandom.uuid, SecureRandom.uuid])
    page = assert_ranking(token, FEED, 'a2:14', 'a1:7', 'x:6')
    @conn.exec_params("INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a1', 'f2', 'rm', 'smile', clock_timestamp())",
                      [SecureRandom.uuid])
    @conn.exec("UPDATE users SET status='deleted' WHERE id='f2'")
    assert_ranking(token, FEED, 'a2:14', 'a1:7', 'x:6')

    cursor = page['items'][1]
    before = URI.encode_www_form(beforeScore: cursor['score'], beforePostedAt: cursor['postedAt'], beforeId: cursor['id'])
    assert_ranking(token, "#{FEED}?#{before}", 'x:6')
    request_json('GET', "#{FEED}?beforeScore=7", token:)
    assert_status 400
    request_json('GET', "#{FEED}?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1", token:)
    assert_status 400

    request_json('GET', '/api/feeds/friends', token:)
    assert_status 200
    body['items'].each { |item| refute item.key?('score'), "friends feed returned a score for #{item['id']}" }
  end

  private

  def assert_ranking(token, path, *expected)
    request_json('GET', path, token:)
    assert_status 200
    response = body
    got = response['items'].map do |item|
      assert_kind_of Integer, item['score'], "#{path}: post #{item['id']} has no score"
      "#{item['id']}:#{item['score']}"
    end
    assert_equal expected, got, path
    response
  end
end
