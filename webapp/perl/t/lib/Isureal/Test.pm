package Isureal::Test;
use v5.44;
use utf8;

use Exporter 'import';
use File::Spec;
use FindBin;
use Imager;
use Mojo::File qw(path);
use Mojo::Pg;
use Test2::V0;
use Test::Mojo;
use Isureal::Session qw(create_login_session);
use Isureal::Util qw(new_uuid sha256_hex);

our @EXPORT_OK = qw(
    SECRET_KEY_HASH
    require_database
    reset_schema
    test_db
    test_app
    insert_user
    login_token
    bearer
    jpeg_data
    concurrently
);

# テスト用ユーザーのシークレットキーは "secret"
use constant SECRET_KEY_HASH => sha256_hex('secret');

my $SQL_DIR = path(__FILE__)->to_abs->dirname->dirname->dirname->dirname->dirname->child('sql');

# DB を使うテストは ISUREAL_TEST_DATABASE=1 のときだけ実行する。
# ISUREAL_DB_* の DB のテーブルを作り直すので、使い捨ての DB を指定すること
sub require_database () {
    skip_all 'ISUREAL_TEST_DATABASE is unset' unless $ENV{ISUREAL_TEST_DATABASE};
}

my $pg;

sub test_pg () {
    return $pg //= do {
        require Isureal::Config;
        my $config = Isureal::Config->from_env;
        Mojo::Pg->new->dsn($config->database_dsn)->username($config->{db_user})->password($config->{db_password});
    };
}

sub test_db () { test_pg()->db }

sub reset_schema () {
    test_db()->query(q{SET client_min_messages = warning});
    test_pg()->db->dbh->do($SQL_DIR->child('001-schema.sql')->slurp('UTF-8'));
}

# 行動ログは TAP の出力に混ざらないように捨てる
sub test_app () {
    my $t = Test::Mojo->new('Isureal');
    $t->app->log->level('fatal');
    open my $null, '>', File::Spec->devnull or die "devnull: $!";
    $t->app->action_log->output($null);
    return $t;
}

sub insert_user (%args) {
    my $id = $args{id} // new_uuid();
    test_db()->query(
        q{INSERT INTO users (id, username, secret_key_hash, display_name, region, account_type) VALUES ($1, $2, $3, $4, $5, $6)},
        $id, $args{username}, SECRET_KEY_HASH, $args{display_name} // $args{username},
        $args{region} // 'asia', $args{account_type} // 'regular',
    );
    return $id;
}

sub login_token ($user_id) {
    return create_login_session(test_db(), $user_id)->{token};
}

sub bearer ($token) {
    return { Authorization => "Bearer $token" };
}

# $width x $height の JPEG。大きさが同じなら同じバイト列になる
sub jpeg_data ($width = 64, $height = $width) {
    my $image = Imager->new(xsize => $width, ysize => $height);
    $image->box(filled => 1, color => [($width * 7) % 256, ($height * 13) % 256, 128]);
    $image->write(data => \my $data, type => 'jpeg') or die Imager->errstr;
    return $data;
}

# $code を $count 個のプロセスで同時に実行し（引数はプロセスの番号）、それぞれの返り値（HTTP ステータスなど）を返す
sub concurrently ($count, $code) {
    my @readers;
    for my $i (0 .. $count - 1) {
        pipe my $reader, my $writer or die "pipe: $!";
        my $pid = fork // die "fork: $!";
        if ($pid == 0) {
            close $reader;
            undef $pg;
            print {$writer} $code->($i);
            close $writer;
            require POSIX;
            POSIX::_exit(0);
        }
        close $writer;
        push @readers, [$pid, $reader];
    }
    my @results;
    for my $child (@readers) {
        my ($pid, $reader) = @$child;
        push @results, do { local $/; scalar <$reader> };
        waitpid $pid, 0;
    }
    return @results;
}

1;
