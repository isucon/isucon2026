#!/bin/bash
set -euo pipefail
shared_buffers_mib=$(python3 - "$(dirname "$0")/runtime.json" <<'PY_CONFIG'
import json,sys
value=json.load(open(sys.argv[1]))['primarySharedBuffersMiB']
assert type(value) is int and 128 <= value <= 1536
print(value)
PY_CONFIG
)
config=/etc/postgresql/16/main/conf.d/astra.conf
mkdir -p "$(dirname "$config")"
cat >"$config.new" <<CONF
shared_buffers = '${shared_buffers_mib}MB'
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
for migration in /home/isucon/astra-deploy/0??-*.sql; do
    sudo -u postgres psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname isureal --file "$migration"
done
