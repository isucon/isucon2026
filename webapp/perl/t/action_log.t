use v5.44;
use utf8;
use Test2::V0;
use Mojo::JSON qw(decode_json);
use Mojolicious;
use Test::Mojo;
use Isureal::APIError;
use Isureal::ActionLog;
use Isureal::Controller;

my sub new_app ($min_level) {
    my $app = Mojolicious->new;
    $app->log->level('fatal');
    $app->controller_class('Isureal::Controller');
    open my $output, '>', \my $buffer or die;
    my $action_log = Isureal::ActionLog->new(min_level => $min_level, output => $output);
    $app->hook(after_dispatch => sub ($c) { $action_log->record($c) });

    my $r = $app->routes;
    $r->post('/api/auth/login' => sub ($c) {
        my $input = $c->json_body(username => 'string', secretKey => 'string');
        $c->stash('isureal.username' => $input->{username});
        $c->render_error(Isureal::APIError->new(401, 'invalid_credentials'));
    })->name('login');
    $r->post('/api/posts' => sub ($c) {
        $c->stash('isureal.session' => { user_id => 'user-1' });
        $c->rendered(201);
    })->name('create_post');
    $r->get('/api/feeds/<#feed>' => sub ($c) {
        $c->stash('isureal.session' => { user_id => 'user-1' });
        $c->rendered(200);
    })->name('get_feed');
    $r->get('/api/users/<#userId>' => sub ($c) { die "boom\n" })->name('get_user');
    $r->put('/api/me/profile-image' => sub ($c) {
        $c->stash('isureal.abuse' => 'invalid_image');
        $c->render_error(Isureal::APIError->new(400, 'invalid_profile_image'));
    })->name('put_profile_image');
    $r->get('/api/images/<#imageId>' => sub ($c) {
        $c->stash('isureal.abuse' => 'invalid_signature');
        $c->render_error(Isureal::APIError->new(403, 'image_forbidden'));
    })->name('get_image');
    return (Test::Mojo->new($app), \$buffer);
}

my sub entries ($buffer) {
    return [map { decode_json($_) } split /\n/, $$buffer];
}

my %headers = ('X-Client-IP' => '203.0.113.7');
my ($t, $buffer) = new_app('debug');
$t->post_ok('/api/auth/login' => \%headers => json => { username => 'alice', secretKey => 'wrong' })->status_is(401);
$t->post_ok('/api/auth/login' => \%headers => json => { username => 'Not Valid', secretKey => 'wrong' })->status_is(401);
$t->post_ok('/api/posts' => \%headers => json => {})->status_is(201);
$t->get_ok('/api/feeds/friends' => \%headers)->status_is(200);
$t->get_ok('/api/users/someone' => \%headers)->status_is(500);
$t->get_ok('/api/unknown' => \%headers)->status_is(404);
is entries($buffer), [
    { level => 'info',  action => 'login',       status => 401, ip => '203.0.113.7', user_id => undef, username => 'alice' },
    { level => 'info',  action => 'login',       status => 401, ip => '203.0.113.7', user_id => undef, username => undef },
    { level => 'info',  action => 'create_post', status => 201, ip => '203.0.113.7', user_id => 'user-1' },
    { level => 'debug', action => 'get_feed',    status => 200, ip => '203.0.113.7', user_id => 'user-1' },
    { level => 'error', action => 'get_user',    status => 500, ip => '203.0.113.7', user_id => undef },
    { level => 'debug', action => undef,         status => 404, ip => '203.0.113.7', user_id => undef },
];
# JSON のキーは Go と同じく辞書順に並び、status は数値になる
is [split /\n/, $$buffer]->[0],
    '{"action":"login","ip":"203.0.113.7","level":"info","status":401,"user_id":null,"username":"alice"}';

# X-Client-IP がなければ接続元のアドレスを使う
($t, $buffer) = new_app('debug');
$t->post_ok('/api/posts' => json => {})->status_is(201);
$t->post_ok('/api/posts' => { 'X-Client-IP' => '' } => json => {})->status_is(201);
is [map { $_->{ip} } entries($buffer)->@*], ['127.0.0.1', '127.0.0.1'];

# info では読み取り（GET）の行を出さない
($t, $buffer) = new_app('info');
$t->get_ok('/api/feeds/friends' => \%headers)->status_is(200);
$t->post_ok('/api/posts' => \%headers => json => {})->status_is(201);
$t->put_ok('/api/me/profile-image' => \%headers)->status_is(400);
$t->get_ok('/api/images/image-id' => \%headers)->status_is(403);
is [map { [$_->{action}, $_->{abuse}] } entries($buffer)->@*], [
    ['create_post', undef], ['put_profile_image', 'invalid_image'], ['get_image', 'invalid_signature'],
];

done_testing;
