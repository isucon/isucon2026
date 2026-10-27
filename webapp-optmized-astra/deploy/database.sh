#!/bin/bash
set -euo pipefail
config=/etc/postgresql/16/main/conf.d/astra.conf
mkdir -p "$(dirname "$config")"
cat >"$config.new" <<'CONF'
shared_buffers = '512MB'
effective_cache_size = '2GB'
max_connections = 200
max_wal_size = '4GB'
wal_compression = lz4
jit = off
CONF
if ! cmp -s "$config.new" "$config"; then
    mv "$config.new" "$config"
    systemctl restart postgresql@16-main
else
    rm "$config.new"
fi
sudo -u postgres psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname isureal --file /tmp/astra-indexes.sql
