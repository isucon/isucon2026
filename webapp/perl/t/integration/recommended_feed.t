use v5.44;
use utf8;
use Test2::V0;
use lib 't/lib';
use Isureal::Test qw(require_database reset_schema test_db test_app insert_user login_token bearer);
use Isureal::Util qw(new_uuid min_max);
use Mojo::Parameters;

require_database();
reset_schema();

my $db = test_db();
my $t  = test_app();
insert_user(id => $_, username => $_) for qw(viewer f1 f2 a1 a2 x);
my $token = login_token('viewer');
$db->query(q{INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)});
for my ($i, $author) (builtin::indexed qw(viewer a1 a2 x)) {
    $db->query(q{INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, 'image', 'image/jpeg')}, "$author-image", $author);
    $db->query(
        q{INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at) VALUES ($1, $2, 'w', $3, 'caption', 'friends_of_friends', 'none', $4)},
        $author, $author, "$author-image", sprintf('2026-09-23T00:01:%02dZ', $i),
    );
}
for my $pair ([qw(f1 viewer)], [qw(f2 viewer)], [qw(a1 f1)], [qw(a1 f2)], [qw(a2 f1)], [qw(a1 x)], [qw(a2 x)], [qw(f2 x)]) {
    $db->query(q{INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, $2, $3)}, new_uuid(), min_max(@$pair));
}

my $feed = '/api/feeds/friends-of-friends';
my sub assert_ranking ($path, @expected) {
    $t->get_ok($path => bearer($token))->status_is(200)->content_unlike(qr/"score":"/);
    my $items = $t->tx->res->json('/items');
    ok defined $_->{score}, "$path: post $_->{id} has a score" for @$items;
    is [map {"$_->{id}:$_->{score}"} @$items], \@expected, "$path ranking";
    return $items;
}

assert_ranking($feed, 'a1:7', 'x:6', 'a2:4');
$db->query(q{INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a2', 'f1', 'rm', 'smile', clock_timestamp())}, new_uuid());
$db->query(q{INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES ($1, 'a2', 'f1', 'nice', clock_timestamp()), ($2, 'a2', 'x', 'nice', clock_timestamp())}, new_uuid(), new_uuid());
my $page = assert_ranking($feed, 'a2:14', 'a1:7', 'x:6');
$db->query(q{INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a1', 'f2', 'rm', 'smile', clock_timestamp())}, new_uuid());
$db->query(q{UPDATE users SET status='deleted' WHERE id='f2'});
assert_ranking($feed, 'a2:14', 'a1:7', 'x:6');

my $cursor = $page->[1];
my $before = Mojo::Parameters->new(beforeScore => $cursor->{score}, beforePostedAt => $cursor->{postedAt}, beforeId => $cursor->{id});
assert_ranking("$feed?$before", 'x:6');
$t->get_ok("$feed?beforeScore=7" => bearer($token))->status_is(400);
$t->get_ok("$feed?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1" => bearer($token))->status_is(400);
$t->get_ok("$feed?beforeScore=x&beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1" => bearer($token))->status_is(400);

$t->get_ok('/api/feeds/friends' => bearer($token))->status_is(200);
ok !exists $_->{score}, "friends feed has no score for $_->{id}" for $t->tx->res->json('/items')->@*;

done_testing;
