use v5.44;
use utf8;
use Test2::V0;
use Digest::SHA qw(sha256);
use MIME::Base64 qw(encode_base64url);
use lib 't/lib';
use Isureal::Test qw(test_app);
use Isureal::ImageSignature qw(image_signature);

# 不正な URL は DB にアクセスする前に拒否する。
local $ENV{ISUREAL_DB_HOST} = '192.0.2.1';
my $t      = test_app();
my $now    = time;
my $key    = sha256('test-secret');
my $sign   = sub ($exp) { encode_base64url(image_signature($key, 'image', $exp)) };
my $future = $now + 86400;
my %cases  = (
    'missing'              => '',
    'missing signature'    => "exp=$future",
    'missing expiry'       => 'sig=' . $sign->($future),
    'invalid expiry'       => 'exp=tomorrow&sig=x',
    'overflow'             => 'exp=999999999999999999999999&sig=x',
    'leading zero'         => "exp=0$future&sig=x",
    'invalid signature'    => "exp=$future&sig=***",
    'short signature'      => "exp=$future&sig=YQ",
    'newline in signature' => "exp=$future&sig=" . $sign->($future) . '%0A',
    'duplicate expiry'     => "exp=$future&exp=$future&sig=x",
    'duplicate signature'  => "exp=$future&sig=x&sig=y",
    'malformed query'      => "exp=$future&sig=%zz",
    'expired'              => 'exp=' . ($now - 1) . '&sig=' . $sign->($now - 1),
    'expiry boundary'      => "exp=$now&sig=" . $sign->($now),
);
for my $name (sort keys %cases) {
    $t->get_ok("/api/images/image?$cases{$name}")
        ->status_is(403, $name)
        ->header_is('Cache-Control' => 'no-store')
        ->json_is('/error' => 'image_forbidden');
}

done_testing;
