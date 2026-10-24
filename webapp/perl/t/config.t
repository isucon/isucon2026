use v5.44;
use utf8;
use Test2::V0;
use Test::Mojo;
use Isureal::Config;

subtest 'defaults' => sub {
    local @ENV{qw(ISUREAL_REGION ISUREAL_DB_HOST ISUREAL_DB_PORT ISUREAL_DB_NAME)} = (q{}, undef, undef, undef);
    my $config = Isureal::Config->from_env;
    is $config->{region}, 'asia';
    is $config->database_dsn, 'dbi:Pg:dbname=isureal;host=127.0.0.1;port=5432';
};

subtest 'invalid region' => sub {
    local $ENV{ISUREAL_REGION} = 'mars';
    like dies { Test::Mojo->new('Isureal') }, qr/invalid server region: mars/;
};

done_testing;
