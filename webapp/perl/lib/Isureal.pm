package Isureal;
use v5.44;
use utf8;
use Mojo::Base 'Mojolicious';

use Mojo::Pg;
use Isureal::APIError;
use Isureal::AsyncDB;
use Isureal::Config;
use Isureal::User qw(valid_region);

sub startup ($self) {
    my $config = Isureal::Config->from_env;
    die "invalid server region: $config->{region}\n" unless valid_region($config->{region});
    $self->config(isureal => $config);

    $self->config(hypnotoad => {
        listen  => ["http://*:$config->{port}"],
        workers => $config->{workers},
        # 通知は接続を長く保持するので、1 worker で多数の接続を受け付ける
        clients            => 1000,
        inactivity_timeout => 60,
        pid_file           => $self->home->child('hypnotoad.pid')->to_string,
    });
    $self->max_request_size(64 * 1024 * 1024);
    $self->types->type(json => 'application/json');
    $self->secrets(['isureal']);
    $self->controller_class('Isureal::Controller');
    $self->routes->namespaces(['Isureal::Controller']);

    my $pg = Mojo::Pg->new
        ->dsn($config->database_dsn)
        ->username($config->{db_user})
        ->password($config->{db_password})
        ->max_connections(1 + $config->{async_db_connections});
    $pg->on(connection => sub ($pg, $dbh) { $dbh->do(q{SET TIME ZONE 'UTC'}) });
    my $async_db = Isureal::AsyncDB->new(pg => $pg, limit => $config->{async_db_connections});

    $self->helper(pg       => sub ($c) { $pg });
    $self->helper(async_db => sub ($c) { $async_db });
    $self->helper(db       => sub ($c) { $c->stash->{'isureal.db'} //= $pg->db });
    $self->helper(region   => sub ($c) { $config->{region} });

    $self->hook(around_action => sub ($next, $c, $action, $last) {
        try {
            return $next->();
        }
        catch ($e) {
            $c->render_error($e);
            return undef;
        }
    });
    $self->hook(after_dispatch => sub ($c) {
        my $req = $c->req;
        $c->app->log->info(sprintf '%s %s %d', $req->method, $req->url->path, $c->res->code // 0);
    });

    $self->setup_routes;
}

sub setup_routes ($self) {
    my $api = $self->routes->any('/api');

    $api->post('/initialize')->to('initialize#initialize');
    $api->post('/auth/register')->to('account#register');
    $api->post('/auth/login')->to('account#login');
    $api->get('/images/<#imageId>')->to('image#show');
    $api->post('/admin/auth/login')->to('admin#login');

    my $session = $api->under('/')->to('session#authenticate');
    $session->post('/auth/logout')->to('account#logout');
    $session->patch('/me')->to('account#update_me');
    $session->put('/me/profile-image')->to('profile_image#update');
    $session->delete('/me/profile-image')->to('profile_image#remove');

    my $regional = $session->under('/')->to('session#check_region');
    $regional->get('/users')->to('user#search');
    $regional->get('/users/<#userId>')->to('user#show');
    $regional->get('/me/export')->to('export#export_posts');
    $regional->get('/me')->to('account#me');
    $regional->post('/me/account-deletion')->to('account#delete_account');
    $regional->post('/posts')->to('post#create');
    $regional->get('/posting-window/current')->to('posting_window#current');
    $regional->get('/posts/me/current')->to('post#my_current');
    $regional->get('/posts/<#postId>')->to('post#show');
    $regional->patch('/posts/<#postId>')->to('post#update');
    $regional->delete('/posts/<#postId>')->to('post#remove');
    $regional->get('/feeds/<#feed>')->to('feed#show');
    $regional->get('/memories')->to('memory#by_month');
    $regional->get('/memories/<#date>')->to('memory#by_date');
    $regional->post('/posts/<#postId>/comments')->to('comment#create');
    $regional->get('/posts/<#postId>/comments')->to('comment#list');
    $regional->delete('/comments/<#commentId>')->to('comment#remove');
    $regional->post('/posts/<#postId>/realmojis')->to('real_moji#replace');
    $regional->get('/posts/<#postId>/realmojis')->to('real_moji#list');
    $regional->delete('/realmojis/<#realMojiId>')->to('real_moji#remove');
    $regional->get('/notifications')->to('notification#stream');
    $regional->post('/friend-requests')->to('friend_request#create');
    $regional->post('/friend-requests/<#requestId>/<#requestAction>')->to('friend_request#transition');
    $regional->get('/relationships/<#kind>')->to('relationship#list');
    $regional->delete('/friendships/<#userId>')->to('relationship#delete_friendship');
    $regional->put('/follows/<#userId>')->to('relationship#follow');
    $regional->delete('/follows/<#userId>')->to('relationship#unfollow');
    $regional->put('/blocks/<#userId>')->to('relationship#block');
    $regional->delete('/blocks/<#userId>')->to('relationship#unblock');

    my $admin = $api->under('/')->to('admin_session#authenticate');
    $admin->post('/admin/auth/logout')->to('admin#logout');
    $admin->put('/admin/users/<#userId>/certification')->to('admin#certify_user');
    $admin->delete('/admin/users/<#userId>/certification')->to('admin#delete_user_certification');
    $admin->post('/admin/posting-window')->to('admin#open_posting_window');

    $api->any('/*whatever' => {whatever => ''})->to('initialize#not_found');
}

1;
