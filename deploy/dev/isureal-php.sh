#!/bin/sh
# PHP 実装の php-fpm をフォアグラウンドで起動する（app-entrypoint.sh から isureal として呼ばれる）
export ISUREAL_PHP_FPM_ERROR_LOG=/proc/self/fd/2
exec php-fpm --nodaemonize --allow-to-run-as-root --fpm-config /app/php/php-fpm.conf
