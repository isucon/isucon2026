use v5.44;
use utf8;
use Test2::V0;
use Mojo::JSON qw(decode_json);
use Time::HiRes qw(sleep);
use lib 't/lib';
use Isureal::IPBan;
use Isureal::Test qw(require_database reset_schema test_db test_app);

require_database();
reset_schema();

my $db = test_db();
my $t  = test_app();
open my $output, '>', \my $buffer or die;
$t->app->action_log->output($output);

my sub ban ($ip, $until) {
    $db->query(q{INSERT INTO ip_bans (ip, banned_until) VALUES ($1, clock_timestamp() + $2::interval)}, $ip, $until);
}
my sub request ($method, $path, $client_ip, $status) {
    my %headers = defined $client_ip ? ('X-Client-IP' => $client_ip) : ();
    my $label   = sprintf '%s %s from %s', $method, $path, $client_ip // 'the peer address';
    $t->request_ok($t->ua->build_tx($method => $path => \%headers => json => { username => 'nobody', secretKey => 'wrong' }))
        ->status_is($status, $label);
    $t->content_is('{"error":"ip_banned"}', $label) if $status == 403;
}

ban('203.0.113.7', '2 minutes');
ban('203.0.113.8', '-1 second');
ban('127.0.0.1',   '2 minutes');
request(POST => '/api/auth/login',     '203.0.113.7',  403);
request(GET  => '/api/images/missing', '203.0.113.7',  403);
request(GET  => '/api/unknown',        '203.0.113.7',  403);
request(POST => '/api/auth/login',     '203.0.113.8',  401);
request(POST => '/api/auth/login',     '198.51.100.1', 401);
# X-Client-IP がなければ接続元（テストでは 127.0.0.1）で判定する
request(POST => '/api/auth/login',     undef,          403);
request(POST => '/api/auth/login',     '',             403);

# BAN した IP からのリクエストも、ルートのアクション名つきで行動ログに出る
my @entries = map { decode_json($_) } split /\n/, $buffer;
is $entries[0], { level => 'info', action => 'login', status => 403, ip => '203.0.113.7', user_id => undef, username => undef };
is $entries[2], { level => 'debug', action => undef, status => 403, ip => '203.0.113.7', user_id => undef };

# 追加した BAN は再読み込みの間隔のうちに反映され、期限を過ぎたら再読み込みを待たずに解ける
ban('198.51.100.1', '600 milliseconds');
sleep Isureal::IPBan::RELOAD_INTERVAL + 0.05;
request(POST => '/api/auth/login', '198.51.100.1', 403);
sleep 0.6;
request(POST => '/api/auth/login', '198.51.100.1', 401);

done_testing;
