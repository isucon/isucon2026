#!/bin/sh
# Python 実装を gunicorn でフォアグラウンド起動する（app-entrypoint.sh から isureal として呼ばれる）
cd /app/python
exec /app/python/.venv/bin/gunicorn
