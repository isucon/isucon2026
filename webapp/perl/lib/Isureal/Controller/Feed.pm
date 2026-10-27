package Isureal::Controller::Feed;
use v5.44;
use utf8;
use Mojo::Base 'Isureal::Controller';

use Isureal::APIError;
use Isureal::Post qw(select_posts_with_author load_viewer_context can_view post_response);

sub show ($c) {
    my $feed             = $c->param('feed');
    my $before_posted_at = $c->query_time('beforePostedAt');
    my $before_id        = $c->query_string('beforeId');
    Isureal::APIError->throw(400, 'invalid_post') unless $feed =~ /\A(?:friends|friends-of-friends|public|following)\z/;
    Isureal::APIError->throw(400, 'invalid_pagination') if defined $before_posted_at xor $before_id ne '';

    my $db     = $c->db;
    my $viewer = load_viewer_context($db, $c->current_session->{user_id})
        or return $c->render(json => { items => [], hasMore => false });
    my %following;
    if ($feed eq 'following') {
        my $followee_ids = $db->query(q{SELECT followee_id FROM follows WHERE follower_id = $1}, $viewer->{id})->arrays;
        $following{$_->[0]} = true for @$followee_ids;
    }
    my $posts = select_posts_with_author($db, <<~'SQL', $viewer->{id}, $before_posted_at && $before_posted_at->to_string, $before_id);
           AND p.author_id <> $1 AND w.ended_at IS NULL
           AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
           ORDER BY p.posted_at DESC, p.id DESC
        SQL
    my @items;
    for my $post (@$posts) {
        next unless can_view($viewer, $post->{post}) && matches_feed($viewer, \%following, $post->{post}, $feed);
        push @items, post_response($post->{post}, $post->{author});
        last if @items == 51;
    }
    my $has_more = @items > 50;
    splice @items, 50 if $has_more;
    $c->render(json => { items => \@items, hasMore => $has_more });
}

sub matches_feed ($viewer, $following, $post, $feed) {
    my $author_id = $post->{author_id};
    return $post->{visibility} eq 'public' if $feed eq 'public';
    return !!$following->{$author_id}       if $feed eq 'following';
    return !!$viewer->{friends}{$author_id} if $feed eq 'friends';
    return !$viewer->{friends}{$author_id} && !!$viewer->{friends_of_friends}{$author_id} if $feed eq 'friends-of-friends';
    return false;
}

1;
