use v5.44;
use utf8;
use Test2::V0;
use IO::Uncompress::Unzip qw($UnzipError);
use Mojo::JSON qw(decode_json);
use Mojo::Promise;
use lib 't/lib';
use Isureal::Test qw(require_database reset_schema test_db test_app insert_user login_token bearer jpeg_data concurrently);
use Isureal::Util qw(new_uuid sha256_hex);

require_database();
reset_schema();

my $db = test_db();
my $t  = test_app();

my sub register ($username, $region = 'asia') {
    $t->post_ok('/api/auth/register' => json => { username => $username, displayName => "\u$username", region => $region })
        ->status_is(201);
    my $registered = $t->tx->res->json;
    $t->post_ok('/api/auth/login' => json => { username => $username, secretKey => $registered->{secretKey} })->status_is(200);
    return { id => $registered->{user}{id}, token => $t->tx->res->json('/token'), secret_key => $registered->{secretKey} };
}

my sub image_form ($fields, $data = jpeg_data()) {
    return (form => { %$fields, image => { content => $data, filename => 'image.jpg', 'Content-Type' => 'image/jpeg' } });
}

subtest 'registration and session' => sub {
    $t->post_ok('/api/initialize')->status_is(200)->json_is('/language' => 'perl') if $ENV{ISUREAL_TEST_INITIALIZE};
    my $alice = register('alice');
    $t->post_ok('/api/auth/register' => json => { username => 'alice', displayName => 'dup', region => 'asia' })
        ->status_is(409)->json_is('/error' => 'username_taken');
    $t->post_ok('/api/auth/register' => json => { username => 'Alice', displayName => 'x', region => 'asia' })
        ->status_is(400)->json_is('/error' => 'invalid_registration');
    $t->post_ok('/api/auth/register' => json => { username => 1, displayName => 'x', region => 'asia' })->status_is(400);
    $t->post_ok('/api/auth/register' => { 'Content-Type' => 'text/plain' } => 'x')->status_is(415);
    $t->post_ok('/api/auth/login' => json => { username => 'alice', secretKey => 'wrong' })
        ->status_is(401)->json_is('/error' => 'invalid_credentials');

    $t->get_ok('/api/me')->status_is(401)->json_is('/error' => 'invalid_session');
    $t->get_ok('/api/me' => { Authorization => 'Basic x' })->status_is(401);
    $t->get_ok('/api/me' => bearer($alice->{token}))->status_is(200)
        ->json_is('/username' => 'alice')->json_is('/profileImageUrl' => undef)->json_is('/accountType' => 'regular');

    # ログインし直すと以前のセッションは無効になる
    $t->post_ok('/api/auth/login' => json => { username => 'alice', secretKey => $alice->{secret_key} })->status_is(200);
    my $token = $t->tx->res->json('/token');
    $t->get_ok('/api/me' => bearer($alice->{token}))->status_is(401);
    $t->post_ok('/api/auth/logout' => bearer($token))->status_is(204);
    $t->get_ok('/api/me' => bearer($token))->status_is(401);
    is $db->query(q{SELECT count(*) FROM login_sessions WHERE active})->array->[0], 0;
};

subtest 'profile' => sub {
    my $bob = register('bob');
    $t->patch_ok('/api/me' => bearer($bob->{token}) => json => {})->status_is(400)->json_is('/error' => 'invalid_profile');
    $t->patch_ok('/api/me' => bearer($bob->{token}) => json => { bio => 'x' x 501 })->status_is(400);
    $t->patch_ok('/api/me' => bearer($bob->{token}) => json => { bio => 'こんにちは', displayName => 'ボブ' })->status_is(200)
        ->json_is('/bio' => 'こんにちは')->json_is('/displayName' => 'ボブ');
    $t->put_ok('/api/me/profile-image' => { bearer($bob->{token})->%*, 'Content-Type' => 'image/png' } => 'x')->status_is(415);
    $t->delete_ok('/api/me/profile-image' => bearer($bob->{token}))->status_is(404);
    for my $case (['not a JPEG' => 'x'], ['too wide' => jpeg_data(513, 8)], ['too tall' => jpeg_data(8, 513)]) {
        my ($name, $image) = @$case;
        $t->put_ok('/api/me/profile-image' => { bearer($bob->{token})->%*, 'Content-Type' => 'image/jpeg' } => $image)
            ->status_is(400, $name)->json_is('/error' => 'invalid_profile_image');
    }
    $t->put_ok('/api/me/profile-image' => { bearer($bob->{token})->%*, 'Content-Type' => 'image/jpeg' } => jpeg_data(512))
        ->status_is(204);
    my $jpeg = jpeg_data(256);
    $t->put_ok('/api/me/profile-image' => { bearer($bob->{token})->%*, 'Content-Type' => 'image/jpeg' } => $jpeg)->status_is(204);
    $t->get_ok('/api/me' => bearer($bob->{token}))->status_is(200);
    my $url = $t->tx->res->json('/profileImageUrl');
    like $url, qr{\A/api/images/[0-9a-f-]{36}\?exp=\d+&sig=[A-Za-z0-9_-]{43}\z};
    my $tx = $t->ua->build_tx(GET => $url => { 'Accept-Encoding' => 'gzip' });
    $tx->res->content->auto_decompress(0);
    $t->request_ok($tx)->status_is(200)->content_is($jpeg)->header_is('Cache-Control' => 'no-store');
    $t->delete_ok('/api/me/profile-image' => bearer($bob->{token}))->status_is(204);
    $t->get_ok($url)->status_is(403);
};

my $admin_token;
subtest 'admin' => sub {
    $db->query(q{INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', $1)}, sha256_hex('password1234'));
    $t->post_ok('/api/admin/auth/login' => json => { username => 'admin', password => 'wrong' })->status_is(401);
    $t->post_ok('/api/admin/auth/login' => json => { username => 'admin', password => 'password1234' })->status_is(200)
        ->json_is('/administratorId' => 'admin1');
    $admin_token = $t->tx->res->json('/token');
    $t->post_ok('/api/admin/posting-window' => json => { region => 'asia', localDate => '2026-10-01' })->status_is(401)
        ->json_is('/error' => 'invalid_administrator_session');
    $t->post_ok('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'mars', localDate => '2026-10-01' })
        ->status_is(400)->json_is('/error' => 'invalid_posting_window');
    $t->post_ok('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'asia', localDate => '2026-02-30' })
        ->status_is(400);
    $t->post_ok('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'asia', localDate => '2026-10-01', onTimeSeconds => 'x' })
        ->status_is(400);
    $t->post_ok('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'asia', localDate => '2026-10-01', onTimeSeconds => 120 })
        ->status_is(201)->json_is('/region' => 'asia')->json_is('/localDate' => '2026-10-01');
    my $window = $t->tx->res->json;
    is Isureal::Util::parse_rfc3339($window->{onTimeDeadline})->epoch - Isureal::Util::parse_rfc3339($window->{startsAt})->epoch, 120;
    $t->post_ok('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'asia', localDate => '2026-10-01' })
        ->status_is(409)->json_is('/error' => 'posting_window_conflict');
    is $db->query(q{SELECT count(*) FROM notifications WHERE posting_window_id = $1}, $window->{id})->array->[0], 2,
        'notifications for active asia users';
};

my %users;
subtest 'relationships' => sub {
    %users = map { $_ => register($_) } qw(carol dave erin frank);
    $users{gina} = register('gina', 'europe');
    my ($carol, $dave, $erin, $frank) = @users{qw(carol dave erin frank)};

    $t->post_ok('/api/friend-requests' => bearer($carol->{token}) => json => { recipientId => $carol->{id} })->status_is(400);
    $t->post_ok('/api/friend-requests' => bearer($carol->{token}) => json => { recipientId => 'nobody' })->status_is(404);
    $t->post_ok('/api/friend-requests' => bearer($carol->{token}) => json => { recipientId => $dave->{id} })->status_is(201)
        ->json_is('/status' => 'pending');
    my $request = $t->tx->res->json('/relationId');
    $t->post_ok('/api/friend-requests' => bearer($carol->{token}) => json => { recipientId => $dave->{id} })->status_is(409);
    $t->get_ok('/api/relationships/incoming-requests' => bearer($dave->{token}))->status_is(200)
        ->json_is('/items/0/relationId' => $request)->json_is('/items/0/user/id' => $carol->{id})->json_is('/items/0/status' => 'pending');
    $t->post_ok("/api/friend-requests/$request/accept" => bearer($carol->{token}))->status_is(403);
    $t->post_ok("/api/friend-requests/$request/unknown" => bearer($dave->{token}))->status_is(403);
    $t->post_ok("/api/friend-requests/$request/accept" => bearer($dave->{token}))->status_is(200)->json_is('/status' => 'accepted');
    $t->post_ok("/api/friend-requests/$request/accept" => bearer($dave->{token}))->status_is(403);

    # 相手からの申請があれば承認になる
    $t->post_ok('/api/friend-requests' => bearer($erin->{token}) => json => { recipientId => $dave->{id} })->status_is(201);
    $t->post_ok('/api/friend-requests' => bearer($dave->{token}) => json => { recipientId => $erin->{id} })->status_is(201)
        ->json_is('/status' => 'accepted');
    $t->get_ok('/api/relationships/friends' => bearer($dave->{token}))->status_is(200)->json_has('/items/1')->json_is('/hasMore' => Mojo::JSON::false);
    $t->get_ok('/api/relationships/friends?beforeId=x' => bearer($dave->{token}))->status_is(400)->json_is('/error' => 'invalid_pagination');
    $t->get_ok('/api/relationships/unknown' => bearer($dave->{token}))->status_is(400)->json_is('/error' => 'invalid_relationship');

    # 同時に申請し合っても友達関係は 1 つだけできる
    my @statuses = concurrently(2, sub ($i) {
        my ($from, $to) = $i == 0 ? ($frank, $carol) : ($carol, $frank);
        test_app()->ua->post('/api/friend-requests' => bearer($from->{token}) => json => { recipientId => $to->{id} })->res->code;
    });
    is \@statuses, [201, 201];
    is $db->query(q{SELECT count(*) FROM friendships WHERE user_low_id = $1 OR user_high_id = $1}, $frank->{id})->array->[0], 1;

    $t->put_ok("/api/follows/$frank->{id}" => bearer($erin->{token}))->status_is(409)->json_is('/error' => 'relationship_conflict');
    $t->put_ok("/api/admin/users/$frank->{id}/certification" => bearer($admin_token))->status_is(204);
    $t->put_ok("/api/admin/users/$frank->{id}/certification" => bearer($admin_token))->status_is(409);
    $t->delete_ok("/api/admin/users/$frank->{id}/certification" => bearer($admin_token))->status_is(409)
        ->json_is('/error' => 'certification_irreversible');
    $t->put_ok("/api/follows/$frank->{id}" => bearer($erin->{token}))->status_is(204);
    $t->put_ok("/api/follows/$frank->{id}" => bearer($erin->{token}))->status_is(409);
    $t->get_ok('/api/relationships/followers' => bearer($frank->{token}))->status_is(200)->json_is('/items/0/user/id' => $erin->{id});
    $t->delete_ok("/api/follows/$frank->{id}" => bearer($erin->{token}))->status_is(204);
    $t->delete_ok("/api/follows/$frank->{id}" => bearer($erin->{token}))->status_is(409);
    $t->put_ok("/api/follows/$frank->{id}" => bearer($erin->{token}))->status_is(204);

    $t->get_ok('/api/me' => bearer($users{gina}{token}))->status_is(403)->json_is('/error' => 'region_mismatch');
};

subtest 'users' => sub {
    my ($carol, $dave, $erin) = @users{qw(carol dave erin)};
    $t->get_ok('/api/users?q=' => bearer($carol->{token}))->status_is(400);
    $t->get_ok('/api/users?q=a&beforeUsername=x' => bearer($carol->{token}))->status_is(400)->json_is('/error' => 'invalid_pagination');
    $t->get_ok('/api/users?q=DAV' => bearer($carol->{token}))->status_is(200)->json_is('/items/0/id' => $dave->{id})->json_is('/items/1' => undef);
    $t->get_ok("/api/users/$dave->{id}" => bearer($carol->{token}))->status_is(200)->json_is('/username' => 'dave');
    $t->put_ok("/api/blocks/$erin->{id}" => bearer($carol->{token}))->status_is(204);
    $t->put_ok("/api/blocks/$erin->{id}" => bearer($carol->{token}))->status_is(409);
    $t->get_ok("/api/users/$carol->{id}" => bearer($erin->{token}))->status_is(404)->json_is('/error' => 'relationship_not_found');
    $t->get_ok('/api/users?q=carol' => bearer($erin->{token}))->status_is(200)->json_is('/items' => []);
    $t->delete_ok("/api/blocks/$erin->{id}" => bearer($carol->{token}))->status_is(204);
    $t->delete_ok("/api/blocks/$erin->{id}" => bearer($carol->{token}))->status_is(403)->json_is('/error' => 'relationship_action_forbidden');
};

my %posts;
subtest 'posts, feeds and interactions' => sub {
    my ($carol, $dave, $erin, $frank) = @users{qw(carol dave erin frank)};
    $t->get_ok('/api/posting-window/current' => bearer($dave->{token}))->status_is(200)
        ->json_is('/hasPosted' => Mojo::JSON::false)->json_is('/canPost' => Mojo::JSON::true)->json_is('/deletionCount' => 0);
    $t->get_ok('/api/feeds/friends' => bearer($carol->{token}))->status_is(200)->json_is('/items' => []);
    $t->get_ok('/api/feeds/unknown' => bearer($carol->{token}))->status_is(400);

    for my $spec ([dave => 'friends'], [erin => 'friends_of_friends'], [frank => 'public']) {
        my ($name, $visibility) = @$spec;
        $t->post_ok('/api/posts' => bearer($users{$name}{token}) => image_form({ caption => $name, visibility => $visibility, location_kind => 'approximate', locality => '東京' }))
            ->status_is(201)->json_is('/visibility' => $visibility)->json_is('/location' => { kind => 'approximate', locality => '東京' });
        $posts{$name} = $t->tx->res->json;
    }
    $t->get_ok('/api/posting-window/current' => bearer($dave->{token}))->status_is(200)
        ->json_is('/hasPosted' => Mojo::JSON::true)->json_is('/canPost' => Mojo::JSON::false);
    $t->get_ok('/api/posts/me/current' => bearer($dave->{token}))->status_is(200)->json_is('/id' => $posts{dave}{id});
    $t->get_ok('/api/posts/me/current' => bearer($carol->{token}))->status_is(404);

    # 投稿していないユーザーには他人の投稿が見えない
    $t->get_ok("/api/posts/$posts{dave}{id}" => bearer($carol->{token}))->status_is(404);
    $t->get_ok('/api/feeds/public' => bearer($carol->{token}))->status_is(200)->json_is('/items' => []);
    $t->post_ok('/api/posts' => bearer($carol->{token}) => image_form({ caption => 'carol', visibility => 'friends', location_kind => 'none' }))->status_is(201);
    $posts{carol} = $t->tx->res->json;

    my sub feed_ids ($feed, $user) {
        $t->get_ok("/api/feeds/$feed" => bearer($user->{token}))->status_is(200);
        return [sort map { $_->{author}{username} } $t->tx->res->json('/items')->@*];
    }
    is feed_ids('friends', $carol),            ['dave', 'frank'];
    is feed_ids('friends-of-friends', $carol), ['erin'];
    is feed_ids('public', $carol),             ['frank'];
    is feed_ids('following', $erin),           ['frank'];
    $t->get_ok('/api/feeds/public?beforePostedAt=2000-01-01T00:00:00Z&beforeId=x' => bearer($carol->{token}))->status_is(200)->json_is('/items' => []);
    $t->get_ok('/api/feeds/public?beforePostedAt=x&beforeId=x' => bearer($carol->{token}))->status_is(400);

    $t->post_ok("/api/posts/$posts{dave}{id}/comments" => bearer($carol->{token}) => json => { body => '' })->status_is(400);
    $t->post_ok("/api/posts/$posts{dave}{id}/comments" => bearer($carol->{token}) => json => { body => 'いいね' })->status_is(201)
        ->json_is('/body' => 'いいね')->json_is('/author/id' => $carol->{id});
    my $comment = $t->tx->res->json('/id');
    $t->post_ok("/api/posts/$posts{erin}{id}/comments" => bearer($carol->{token}) => json => { body => 'x' })->status_is(403)
        ->json_is('/error' => 'interaction_forbidden');
    $t->get_ok("/api/posts/$posts{dave}{id}/comments" => bearer($dave->{token}))->status_is(200)->json_is('/items/0/id' => $comment);

    $t->post_ok("/api/posts/$posts{dave}{id}/realmojis" => bearer($carol->{token}) => image_form({ reactionType => 'unknown' }))->status_is(400);
    for my $case (['not a JPEG' => 'x'], ['too wide' => jpeg_data(513, 8)], ['too tall' => jpeg_data(8, 513)]) {
        my ($name, $image) = @$case;
        $t->post_ok("/api/posts/$posts{dave}{id}/realmojis" => bearer($carol->{token}) => image_form({ reactionType => 'like' }, $image))
            ->status_is(400, $name)->json_is('/error' => 'invalid_realmoji');
    }
    $t->post_ok("/api/posts/$posts{dave}{id}/realmojis" => bearer($dave->{token}) => image_form({ reactionType => 'like' }))->status_is(403);
    $t->post_ok("/api/posts/$posts{dave}{id}/realmojis" => bearer($carol->{token}) => image_form({ reactionType => 'like' }))->status_is(201);
    my $replaced = $t->tx->res->json;
    $t->post_ok("/api/posts/$posts{dave}{id}/realmojis" => bearer($carol->{token}) => image_form({ reactionType => 'heart' }, jpeg_data(10)))
        ->status_is(201)->json_is('/reactionType' => 'heart');
    my $realmoji = $t->tx->res->json;
    $t->get_ok("/api/posts/$posts{dave}{id}/realmojis" => bearer($dave->{token}))->status_is(200)
        ->json_is('/items/0/id' => $realmoji->{id})->json_is('/items/1' => undef);
    $t->get_ok($realmoji->{imageUrl})->status_is(200)->content_is(jpeg_data(10));
    $t->get_ok($replaced->{imageUrl})->status_is(403);

    $t->delete_ok("/api/comments/$comment" => bearer($erin->{token}))->status_is(403);
    $t->delete_ok("/api/comments/$comment" => bearer($dave->{token}))->status_is(204);
    $t->delete_ok("/api/comments/$comment" => bearer($dave->{token}))->status_is(404)->json_is('/error' => 'interaction_not_found');
    $t->delete_ok("/api/realmojis/$realmoji->{id}" => bearer($carol->{token}))->status_is(204);
    $t->get_ok($realmoji->{imageUrl})->status_is(403);
};

subtest 'memories and export' => sub {
    my $dave = $users{dave};
    $t->post_ok("/api/posts/$posts{dave}{id}/comments" => bearer($users{carol}{token}) => json => { body => 'memory' })->status_is(201);
    $t->get_ok('/api/memories?month=2026-13' => bearer($dave->{token}))->status_is(400)->json_is('/error' => 'invalid_memory_period');
    $t->get_ok('/api/memories/2026-10-32' => bearer($dave->{token}))->status_is(400);
    $t->get_ok('/api/memories?month=2026-10&beforeId=x' => bearer($dave->{token}))->status_is(400)->json_is('/error' => 'invalid_pagination');
    $t->get_ok('/api/memories?month=2026-10' => bearer($dave->{token}))->status_is(200)
        ->json_is('/items/0/id' => $posts{dave}{id})->json_is('/items/0/comments/0/body' => 'memory')->json_is('/items/0/realmojis' => [])
        ->json_is('/hasMore' => Mojo::JSON::false);
    $t->get_ok('/api/memories/2026-10-01' => bearer($dave->{token}))->status_is(200)->json_is('/items/0/id' => $posts{dave}{id});
    $t->get_ok('/api/memories/2026-10-02' => bearer($dave->{token}))->status_is(200)->json_is('/items' => []);

    $t->get_ok('/api/me/export' => bearer($dave->{token}))->status_is(200)->content_type_is('application/zip')
        ->header_is('Content-Disposition' => 'attachment; filename="isureal-posts.zip"');
    my $zip = $t->tx->res->body;
    my %entries;
    my $unzip = IO::Uncompress::Unzip->new(\$zip) or die $UnzipError;
    for (my $status = 1; $status > 0; $status = $unzip->nextStream) {
        my $name = $unzip->getHeaderInfo->{Name};
        local $/;
        $entries{$name} = <$unzip>;
    }
    is [sort keys %entries], ['images/000001.jpg', 'posts.json'];
    is $entries{'images/000001.jpg'}, jpeg_data();
    my $manifest = decode_json($entries{'posts.json'});
    is $manifest->[0]{id}, $posts{dave}{id};
    is $manifest->[0]{imagePath}, 'images/000001.jpg';
};

subtest 'notifications' => sub {
    my $dave = $users{dave};
    $t->get_ok('/api/notifications' => bearer($users{gina}{token}))->status_is(403);

    my ($stream, $opened);
    my $listen = $t->ua->get_p('/api/notifications' => bearer($dave->{token}))->then(sub ($tx) { $stream = $tx });
    my $open   = Mojo::Promise->timer(0.7)->then(sub {
        $t->ua->post_p('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'asia', localDate => '2026-10-02' });
    })->then(sub ($tx) { $opened = $tx->res->json });
    Mojo::Promise->all($listen, $open)->wait;

    is $stream->res->code, 200;
    like $stream->res->headers->content_type, qr{\Atext/event-stream};
    my ($data) = $stream->res->body =~ /^data: (.+)$/m;
    my $notification = decode_json($data);
    is $notification->{postingWindowId}, $opened->{id};
    is $notification->{region}, 'asia';
    like $notification->{createdAt}, qr/Z\z/;
};

subtest 'account deletion' => sub {
    my $frank = $users{frank};
    $t->post_ok('/api/me/account-deletion' => bearer($frank->{token}))->status_is(204);
    $t->get_ok('/api/me' => bearer($frank->{token}))->status_is(401);
    $t->get_ok($posts{frank}{imageUrl})->status_is(403);
    $t->post_ok('/api/auth/login' => json => { username => 'frank', secretKey => $frank->{secret_key} })->status_is(401);
};

subtest 'admin logout and unknown routes' => sub {
    $t->post_ok('/api/admin/auth/logout' => bearer($admin_token))->status_is(204);
    $t->post_ok('/api/admin/posting-window' => bearer($admin_token) => json => { region => 'asia', localDate => '2026-10-09' })->status_is(401);
    $t->get_ok('/api/unknown')->status_is(404)->json_is('/error' => 'Not Found');
};

done_testing;
