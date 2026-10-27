use v5.44;
use utf8;
use Test2::V0;
use Isureal::User qw(valid_username valid_user_text valid_region user_columns_as extract_user);

ok valid_username($_), "accept $_" for 'alice', 'a.b-c_d', 'x' x 64;
ok !valid_username($_), 'reject ' . ($_ // 'undef') for '', 'Alice', 'a b', 'x' x 65, "alice\n", undef;

ok valid_user_text('日本語', 1, 3);
ok !valid_user_text('日本語', 1, 2);
ok valid_user_text('', 0, 10);
ok !valid_user_text('', 1, 10);
ok !valid_user_text(undef, 0, 10);

ok valid_region($_), "accept $_" for qw(america europe asia);
ok !valid_region($_), "reject $_" for 'mars', 'Asia', '';

like user_columns_as('u', 'author'), qr/\Au\.id AS "author\.id", u\.username AS "author\.username"/;

my $row = {
    id => 'post', 'author.id' => 'user', 'author.username' => 'alice', 'author.secret_key_hash' => '0' x 64,
    map { ("author.$_" => $_) } qw(display_name bio profile_image_id region account_type status),
};
my $user = extract_user($row, 'author');
is $user->{id}, 'user';
is $user->{image_key}, "\0" x 32;
is [sort keys %$row], ['id'], 'prefixed columns are removed from the row';

done_testing;
