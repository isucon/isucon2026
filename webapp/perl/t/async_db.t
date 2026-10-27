use v5.44;
use utf8;
use Test2::V0;
use Mojo::Promise;
use Isureal::AsyncDB;

package FakeDatabase {
    sub new ($class, $pg) { bless { pg => $pg }, $class }

    sub query_p ($self, $sql, @args) {
        my $pg = $self->{pg};
        $pg->{running}++;
        $pg->{max_running} = $pg->{running} if $pg->{running} > ($pg->{max_running} // 0);
        return Mojo::Promise->timer(0.01)->then(sub {
            $pg->{running}--;
            die "query failed\n" if $sql eq 'fail';
            return "$sql:@args";
        });
    }
}

package FakePg {
    sub new ($class) { bless { running => 0, broken => 0 }, $class }

    sub db ($self) {
        die "connection refused\n" if $self->{broken};
        return FakeDatabase->new($self);
    }
}

subtest 'limits concurrent queries' => sub {
    my $pg       = FakePg->new;
    my $async_db = Isureal::AsyncDB->new(pg => $pg, limit => 2);
    my @results;
    Mojo::Promise->all(map { my $i = $_; $async_db->query_p('q', $i)->then(sub ($r) { push @results, $r }) } 1 .. 5)->wait;
    is [sort @results], [map {"q:$_"} 1 .. 5];
    is $pg->{max_running}, 2;
    is $async_db->active, 0;
};

subtest 'rejects failed queries' => sub {
    my $async_db = Isureal::AsyncDB->new(pg => FakePg->new, limit => 1);
    my $error;
    $async_db->query_p('fail')->catch(sub ($e) { $error = $e })->wait;
    is $error, "query failed\n";
    is $async_db->active, 0;
};

subtest 'does not leak slots when connecting fails' => sub {
    my $pg       = FakePg->new;
    my $async_db = Isureal::AsyncDB->new(pg => $pg, limit => 1);
    $pg->{broken} = 1;
    my $error;
    $async_db->query_p('q')->catch(sub ($e) { $error = $e })->wait;
    is $error, "connection refused\n";
    is $async_db->active, 0;
    $pg->{broken} = 0;
    my $result;
    $async_db->query_p('q', 1)->then(sub ($r) { $result = $r })->wait;
    is $result, 'q:1';
};

done_testing;
