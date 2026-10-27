#!/bin/sh
# Perl 実装を hypnotoad でフォアグラウンド起動する（app-entrypoint.sh から isureal として呼ばれる）
export PERL5LIB=/app/perl/local/lib/perl5
exec perl /app/perl/local/bin/hypnotoad -f /app/perl/script/isureal
