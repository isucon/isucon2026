#!/bin/sh
# Ruby 実装を Puma でフォアグラウンド起動する（app-entrypoint.sh から isureal として呼ばれる）
cd /app/ruby
exec bundle exec puma -C config/puma.rb
