#!/bin/sh
# Node.js 実装をフォアグラウンド起動する（app-entrypoint.sh から isureal として呼ばれる）
cd /app/nodejs
exec node dist/main.js
