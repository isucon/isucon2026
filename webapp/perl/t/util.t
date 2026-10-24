use v5.44;
use utf8;
use Test2::V0;
use Isureal::Util qw(new_uuid sha256_hex secure_compare min_max placeholders rfc3339 moment parse_rfc3339 parse_date);

subtest 'new_uuid' => sub {
    my $uuid = new_uuid();
    like $uuid, qr/\A[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\z/;
    isnt new_uuid(), $uuid;
};

subtest 'sha256_hex' => sub {
    is sha256_hex('secret'), '2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b';
    is sha256_hex('日本'), sha256_hex(do { my $s = '日本'; utf8::encode($s); $s }), 'characters are hashed as UTF-8';
};

subtest 'secure_compare' => sub {
    ok secure_compare('abc', 'abc');
    ok !secure_compare('abc', 'abd');
    ok !secure_compare('abc', 'abcd');
};

subtest 'min_max and placeholders' => sub {
    is [min_max('b', 'a')], ['a', 'b'];
    is [min_max('a', 'b')], ['a', 'b'];
    is placeholders(3), '$1, $2, $3';
    is placeholders(2, 3), '$4, $5';
};

subtest 'timestamps' => sub {
    is rfc3339('2026-10-24 01:02:03.12+00'), '2026-10-24T01:02:03.12Z';
    is rfc3339('2026-10-24 01:02:03+00'),    '2026-10-24T01:02:03Z';
    is rfc3339(undef), undef;
    like dies { rfc3339('2026-10-24 01:02:03+09') }, qr/unexpected timestamp format/;
    is moment('2026-10-24 01:02:03.5+00')->epoch, 1792803723;
};

subtest 'parse_rfc3339' => sub {
    is parse_rfc3339('2026-10-24T10:00:00.123456789+09:00')->to_string, '2026-10-24T01:00:00.123456Z';
    is parse_rfc3339('2026-10-24T01:00:00Z')->to_string, '2026-10-24T01:00:00Z';
    is parse_rfc3339($_), undef, "reject $_" for '', 'tomorrow', '2026-10-24', '2026-10-24 01:00:00Z', '2026-13-01T00:00:00Z';
};

subtest 'parse_date' => sub {
    is parse_date('2026-02-28')->strftime('%Y-%m-%d'), '2026-02-28';
    is parse_date($_), undef, 'reject ' . ($_ // 'undef') for '2026-02-30', '2026-2-1', '20260201', undef;
};

done_testing;
