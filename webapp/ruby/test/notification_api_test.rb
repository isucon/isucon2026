# frozen_string_literal: true

require 'net/http'
require 'puma'
require 'securerandom'
require_relative 'test_helper'

# SSE は Puma の partial hijack を使うので、実際に Puma を起動して確かめる。
class NotificationAPITest < IntegrationTest
  def setup
    super
    @server = Puma::Server.new(@app, nil, min_threads: 1, max_threads: 1, log_writer: Puma::LogWriter.null)
    @server.add_tcp_listener('127.0.0.1', 0)
    @port = @server.connected_ports.first
    @server.run
  end

  def teardown
    @server&.stop(true)
    super
  end

  def open_stream(token)
    queue = Queue.new
    thread = Thread.new do
      Net::HTTP.start('127.0.0.1', @port, read_timeout: 10) do |http|
        request = Net::HTTP::Get.new('/api/notifications', 'Authorization' => "Bearer #{token}")
        http.request(request) do |response|
          queue << [:headers, response.code, response['content-type']]
          data = +''
          response.read_body { |chunk| data << chunk }
          queue << [:body, data]
        end
      end
    rescue StandardError => e
      queue << [:error, e]
    end
    [queue, thread]
  end

  def api_get(path, token)
    Net::HTTP.start('127.0.0.1', @port, read_timeout: 5) do |http|
      http.request(Net::HTTP::Get.new(path, 'Authorization' => "Bearer #{token}"))
    end
  end

  def test_streams_one_notification_without_holding_request_threads
    alice = register_and_login('alice')
    bob = register_and_login('bob')
    window_id = SecureRandom.uuid
    ended_id = SecureRandom.uuid
    @conn.exec_params(<<~SQL, [window_id, ended_id])
      INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at)
      VALUES ($1, 'asia', '2026-09-23', clock_timestamp(), clock_timestamp(), NULL),
             ($2, 'asia', '2026-09-22', clock_timestamp(), clock_timestamp(), clock_timestamp())
    SQL
    @conn.exec_params("INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES ($1, $2, $3, clock_timestamp() - interval '1 minute')",
                      [SecureRandom.uuid, alice[:id], window_id])

    streams = Array.new(3) { open_stream(alice[:token]) }
    streams.each do |queue, _|
      assert_equal [:headers, '200', 'text/event-stream'], queue.pop
    end
    assert_equal '200', api_get('/api/me', alice[:token]).code

    @conn.exec_params(<<~SQL, [SecureRandom.uuid, alice[:id], ended_id, SecureRandom.uuid, bob[:id], window_id])
      INSERT INTO notifications (id, user_id, posting_window_id, created_at)
      VALUES ($1, $2, $3, clock_timestamp()), ($4, $5, $6, clock_timestamp())
    SQL
    sleep 0.7
    streams.each { |queue, _| assert_empty queue }

    new_window_id = SecureRandom.uuid
    notification_id = SecureRandom.uuid
    @conn.exec_params('UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = $1', [window_id])
    @conn.exec_params("INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', '2026-09-24', clock_timestamp(), clock_timestamp())",
                      [new_window_id])
    @conn.exec_params('INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES ($1, $2, $3, clock_timestamp())',
                      [notification_id, alice[:id], new_window_id])
    streams.each do |queue, thread|
      kind, data = queue.pop
      assert_equal :body, kind
      assert_match(/\Adata: \{.*\}\n\n\z/, data)
      notification = JSON.parse(data.delete_prefix('data: '))
      assert_equal [notification_id, new_window_id, 'asia'], notification.values_at('id', 'postingWindowId', 'region')
      thread.join
    end
  end

  def test_region_change_ends_stream
    alice = register_and_login('alice')
    queue, thread = open_stream(alice[:token])
    assert_equal [:headers, '200', 'text/event-stream'], queue.pop
    @conn.exec_params("UPDATE users SET region = 'europe' WHERE id = $1", [alice[:id]])
    assert_equal [:body, ''], queue.pop
    thread.join
  end
end
