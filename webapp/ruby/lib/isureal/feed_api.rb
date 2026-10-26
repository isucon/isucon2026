# frozen_string_literal: true

module Isureal
  class App
    FEEDS = %w[friends friends-of-friends public following].freeze

    GET_FEED_REQUEST = {
      feed: { param: 'feed' },
      before_posted_at: { query: 'beforePostedAt', type: :time },
      before_id: { query: 'beforeId' },
    }.freeze

    def get_feed_handler
      input = bind(GET_FEED_REQUEST)
      raise APIError.new(400, 'invalid_post') unless FEEDS.include?(input[:feed])
      raise APIError.new(400, 'invalid_pagination') if input[:before_posted_at].nil? != input[:before_id].empty?

      viewer = load_viewer_context(db, current_session.user_id)
      return json_response(200, { items: [], hasMore: false }) if viewer.nil?

      following = {}
      if input[:feed] == 'following'
        db.select_values('SELECT followee_id FROM follows WHERE follower_id = $1', viewer.id).each { |id| following[id] = true }
      end
      posts = select_posts_with_author(db, <<~SQL, viewer.id, input[:before_posted_at], input[:before_id])
        AND p.author_id <> $1 AND w.ended_at IS NULL
        AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
        ORDER BY p.posted_at DESC, p.id DESC
      SQL
      items = []
      posts.each do |post|
        next if !viewer.can_view?(post) || !matches_feed?(viewer, following, post, input[:feed])

        items << post_response(post, post.author)
        break if items.length == 51
      end
      has_more = items.length > 50
      items = items.first(50) if has_more
      json_response(200, { items:, hasMore: has_more })
    end

    def matches_feed?(viewer, following, post, feed)
      case feed
      when 'public' then post.visibility == 'public'
      when 'following' then following[post.author_id]
      when 'friends' then viewer.friends[post.author_id]
      when 'friends-of-friends' then !viewer.friends[post.author_id] && viewer.friends_of_friends[post.author_id]
      else false
      end
    end
  end
end
