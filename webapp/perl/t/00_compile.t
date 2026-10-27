use v5.44;
use utf8;
use Test2::V0;
use Mojo::File qw(path);

my $lib = path(__FILE__)->to_abs->dirname->dirname->child('lib');
for my $file ($lib->list_tree->grep(qr/\.pm\z/)->each) {
    my $module = $file->to_rel($lib) =~ s{/}{::}gr =~ s/\.pm\z//r;
    ok lives { require $file->to_rel($lib)->to_string }, "load $module" or note $@;
}

done_testing;
