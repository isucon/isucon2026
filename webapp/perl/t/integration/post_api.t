use v5.44;
use utf8;
use Test2::V0;
use lib 't/lib';
use Isureal::Test qw(require_database reset_schema test_db test_app insert_user login_token bearer jpeg_data concurrently);
use Mojo::JSON;
use Isureal::Util qw(new_uuid);

require_database();
reset_schema();

my $db        = test_db();
my $user_id   = insert_user(username => 'alice', display_name => 'アリス');
my $token     = login_token($user_id);
my $window_id = new_uuid();
my $t         = test_app();
my $jpeg      = jpeg_data();

my sub post_form (%location) {
    my $image = delete $location{image} // $jpeg;
    return (form => {
        caption    => 'こんにちは',
        visibility => 'public',
        %location,
        image => { content => $image, filename => 'image.jpg', 'Content-Type' => 'image/jpeg' },
    });
}
# form の値は送信時に消費されるので、毎回作り直す
my sub exact () { post_form(location_kind => 'exact', latitude => 35, longitude => 139) }

$t->post_ok('/api/posts' => exact())->status_is(401);
$t->post_ok('/api/posts' => bearer($token) => exact())->status_is(409)->json_is('/error' => 'post_slot_unavailable');
$db->query(
    q{INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', '2026-09-23', clock_timestamp() - interval '1 minute', clock_timestamp() + interval '1 minute')},
    $window_id,
);
$t->post_ok('/api/posts' => bearer($token) => post_form(location_kind => 'exact', latitude => 91, longitude => 139))
    ->status_is(400)->json_is('/error' => 'invalid_post');

# 画像は JPEG として復号でき、縦横とも 4096 px 以下でなければならない
for my $case (
    ['not a JPEG' => 'not a jpeg'],
    ['truncated'  => substr(jpeg_data(64), 0, -100)],
    ['too wide'   => jpeg_data(4097, 8)],
    ['too tall'   => jpeg_data(8, 4097)],
) {
    my ($name, $image) = @$case;
    $t->post_ok('/api/posts' => bearer($token) => post_form(location_kind => 'none', image => $image))
        ->status_is(400, $name)->json_is('/error' => 'invalid_post');
}
is $db->query(q{SELECT count(*) FROM image_objects})->array->[0], 0, 'rejected images are not stored';

my @statuses = concurrently(8, sub { test_app()->ua->post('/api/posts' => bearer($token) => exact())->res->code });
is [sort @statuses], [201, (409) x 7], 'only one post is created in a posting window';

my $created = $db->query(q{SELECT id FROM posts WHERE author_id = $1}, $user_id)->array->[0];
$t->get_ok("/api/posts/$created" => bearer($token))->status_is(200)
    ->json_is('/author/id' => $user_id)
    ->json_is('/author/displayName' => 'アリス')
    ->json_is('/postingWindowId' => $window_id)
    ->json_is('/caption' => 'こんにちは')
    ->json_is('/onTime' => true)
    ->json_is('/lateSeconds' => 0)
    ->json_is('/location' => { kind => 'exact', latitude => 35, longitude => 139 })
    ->json_like('/postedAt' => qr/\A\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z\z/);
my $post = $t->tx->res->json;
is $db->query(q{SELECT data FROM image_objects WHERE owner_user_id = $1}, $user_id)->array->[0], $jpeg, 'stored image';
$t->get_ok($post->{imageUrl})->status_is(200)->content_type_is('image/jpeg')->content_is($jpeg);

my $path = "/api/posts/$post->{id}";
$t->patch_ok($path => json => { caption => 'edited' })->status_is(401);
$t->delete_ok($path)->status_is(401);
$t->patch_ok('/api/posts/missing' => bearer($token) => json => { caption => 'edited' })->status_is(404);
$t->delete_ok('/api/posts/missing' => bearer($token))->status_is(404);
for my $invalid ('{', '{}', '{"visibility":"invalid"}') {
    $t->patch_ok($path => { bearer($token)->%*, 'Content-Type' => 'application/json' } => $invalid)->status_is(400);
}
my $other = login_token(insert_user(username => 'other'));
$t->patch_ok($path => bearer($other) => json => { caption => 'edited' })->status_is(403)->json_is('/error' => 'post_not_editable');
$t->delete_ok($path => bearer($other))->status_is(403);
$t->patch_ok($path => bearer($token) => json => { caption => 'edited' })->status_is(200)
    ->json_is('/caption' => 'edited')
    ->json_is('/visibility' => $post->{visibility})
    ->json_is('/postedAt' => $post->{postedAt});
is [split /\?/, $t->tx->res->json('/imageUrl')]->[0], [split /\?/, $post->{imageUrl}]->[0];
$t->patch_ok($path => bearer($token) => json => { visibility => 'friends' })->status_is(200);

$db->query(q{CREATE FUNCTION reject_visibility() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject visibility'; END $$});
$db->query(q{CREATE TRIGGER reject_visibility BEFORE UPDATE OF visibility ON posts FOR EACH ROW EXECUTE FUNCTION reject_visibility()});
$t->patch_ok($path => bearer($token) => json => { caption => 'rolled back', visibility => 'public' })->status_is(500);
$db->query(q{DROP FUNCTION reject_visibility() CASCADE});
is $db->query(q{SELECT caption FROM posts WHERE id = $1}, $post->{id})->array->[0], 'edited', 'partial update was rolled back';
$t->patch_ok($path => bearer($token) => json => { caption => '' })->status_is(200);

my $europe_window_id = new_uuid();
$db->query(
    q{INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'europe', '2026-09-23', clock_timestamp()-interval '1 minute', clock_timestamp()-interval '10 seconds')},
    $europe_window_id,
);
$db->query(q{UPDATE users SET region='europe' WHERE id=$1}, $user_id);
my sub europe_app () {
    local $ENV{ISUREAL_REGION} = 'europe';
    return test_app();
}
my $asia = $t;
$t = europe_app();
$t->post_ok('/api/posts' => bearer($token) => exact())->status_is(409);

@statuses = concurrently(8, sub { europe_app()->ua->delete($path => bearer($token))->res->code });
is [sort @statuses], [204, (404) x 7], 'only one delete succeeds';
$t->get_ok($path => bearer($token))->status_is(404);
$t->get_ok($post->{imageUrl})->status_is(403);
$t->patch_ok($path => bearer($token) => json => { caption => 'edited' })->status_is(404);

$db->query(q{UPDATE posting_windows SET on_time_deadline=clock_timestamp()-interval '10 seconds' WHERE id=$1}, $window_id);
$t->post_ok('/api/posts' => bearer($token) => exact())->status_is(201)->json_is('/onTime' => Mojo::JSON::false);
my $late = $t->tx->res->json;
ok $late->{lateSeconds} >= 10, 'late seconds';
$db->query(q{UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id=$1}, $europe_window_id);
$t->patch_ok("/api/posts/$late->{id}" => bearer($token) => json => { caption => 'edited' })->status_is(403);
$t->delete_ok("/api/posts/$late->{id}" => bearer($token))->status_is(204);
$db->query(q{UPDATE users SET region='asia' WHERE id=$1}, $user_id);
$t = $asia;
$t->post_ok('/api/posts' => bearer($token) => exact())->status_is(409);
$db->query(q{UPDATE posting_windows SET ended_at=clock_timestamp() WHERE id=$1}, $window_id);
$t->post_ok('/api/posts' => bearer($token) => exact())->status_is(409);

$db->query(
    q{INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', '2026-09-24', clock_timestamp(), clock_timestamp()+interval '1 minute')},
    new_uuid(),
);
$db->query(q{CREATE FUNCTION reject_post() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject post'; END $$});
$db->query(q{CREATE TRIGGER reject_post BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION reject_post()});
$t->post_ok('/api/posts' => bearer($token) => exact())->status_is(500);
$db->query(q{DROP FUNCTION reject_post() CASCADE});
is $db->query(q{SELECT count(*) FROM image_objects})->array->[0], 2, 'image insert was rolled back';

done_testing;
