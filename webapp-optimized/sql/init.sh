#!/bin/bash

set -eu
cd -- "$(dirname -- "$0")"

if test -f /home/isucon/env.sh; then
	. /home/isucon/env.sh
fi

ISUREAL_DB_HOST=${ISUREAL_DB_HOST:-127.0.0.1}
ISUREAL_DB_PORT=${ISUREAL_DB_PORT:-5432}
ISUREAL_DB_USER=${ISUREAL_DB_USER:-isureal}
ISUREAL_DB_PASSWORD=${ISUREAL_DB_PASSWORD:-isureal}
ISUREAL_DB_NAME=${ISUREAL_DB_NAME:-isureal}

# Only the tables the reset rewrites are analyzed. A database-wide ANALYZE
# samples every image partition and the history tables (about 25 s on the
# large data set) and their statistics do not change.
PGPASSWORD="$ISUREAL_DB_PASSWORD" psql \
	--no-psqlrc \
	--no-password \
	--host="$ISUREAL_DB_HOST" \
	--port="$ISUREAL_DB_PORT" \
	--username="$ISUREAL_DB_USER" \
	--dbname="$ISUREAL_DB_NAME" \
	--set=ON_ERROR_STOP=1 \
	--file=reset.sql \
	--command='ANALYZE users, login_sessions, posting_windows, friendships, friend_requests, follows, block_relations, ip_bans'
