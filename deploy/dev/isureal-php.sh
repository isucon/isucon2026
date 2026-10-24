#!/bin/sh
# PHP 実装の php-fpm をフォアグラウンドで起動する（app-entrypoint.sh から isureal として呼ばれる）
exec php-fpm --nodaemonize --allow-to-run-as-root --fpm-config /app/php/php-fpm.conf
