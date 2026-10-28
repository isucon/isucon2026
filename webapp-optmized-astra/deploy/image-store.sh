#!/bin/bash
# Independent PostgreSQL on existing application VMs. No AWS resources.
set -euo pipefail
runtime="$(dirname "$0")/runtime.json"
region=${1:-asia}
image_host=$(python3 - "$runtime" "$region" <<'PY_CONFIG'
import json,sys
runtime=json.load(open(sys.argv[1])); s=runtime['imageStore']; region=sys.argv[2]
assert s['enabled'] and s['host']=='192.168.145.10' and s['port']==5433
assert region in ('asia','europe','america')
assert region=='asia' or runtime.get('regionalImageStoresEnabled',False)
app=next(app for app in runtime['applications'] if app['region']==region)
assert app['host']=={'asia':'192.168.145.10','europe':'192.168.146.10','america':'192.168.147.10'}[region]
print(app['host'])
PY_CONFIG
)
set -a
. /home/isucon/env.sh
set +a
: "${ISUREAL_DB_USER:=isureal}"
: "${ISUREAL_DB_PASSWORD:=isureal}"
export ISUREAL_DB_USER ISUREAL_DB_PASSWORD
[ "$ISUREAL_DB_USER" != postgres ] || { echo 'Image store requires an application role' >&2; exit 1; }
cluster=astraassets
configuration=/etc/postgresql/16/$cluster
# Debian's systemd template expands every dash into a path separator.
# Reconcile the initial, stopped cluster from the first deployment attempt.
if [ -f /etc/postgresql/16/astra-assets/postgresql.conf ] && [ ! -f "$configuration/postgresql.conf" ]; then
    pg_renamecluster 16 astra-assets "$cluster"
    systemctl reset-failed postgresql@16-astra-assets || true
fi
if [ ! -f "$configuration/postgresql.conf" ]; then
    # The original 16/main database is preserved and remains untouched.
    pg_createcluster 16 "$cluster" --port=5433 --start-conf=auto
fi
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
cat >"$staging/astra.conf" <<CONF
listen_addresses = '127.0.0.1,$image_host'
port = 5433
shared_buffers = '256MB'
effective_cache_size = '2GB'
max_connections = 100
max_wal_size = '2GB'
checkpoint_timeout = '15min'
wal_level = minimal
max_wal_senders = 0
max_replication_slots = 0
wal_compression = lz4
synchronous_commit = on
fsync = on
full_page_writes = on
jit = off
CONF
cat >"$staging/pg_hba.conf" <<'CONF'
local all postgres peer
host isureal_assets all 127.0.0.1/32 scram-sha-256
host isureal_assets all 192.168.145.10/32 scram-sha-256
host isureal_assets all 192.168.146.10/32 scram-sha-256
host isureal_assets all 192.168.147.10/32 scram-sha-256
CONF
changed=false
mkdir -p "$configuration/conf.d"
for name in astra.conf pg_hba.conf; do
    destination="$configuration/$name"
    [ "$name" != astra.conf ] || destination="$configuration/conf.d/$name"
    if ! cmp -s "$staging/$name" "$destination"; then
        install -o postgres -g postgres -m640 "$staging/$name" "$destination"
        changed=true
    fi
done
systemctl daemon-reload
systemctl enable postgresql
if [ "$changed" = true ] || ! systemctl is-active --quiet postgresql@16-astraassets; then
    systemctl restart postgresql@16-astraassets
fi
# Read credentials from the private existing env file, never from command text.
# psql emits command tags, not the dynamically generated password statement.
sudo --preserve-env=ISUREAL_DB_USER,ISUREAL_DB_PASSWORD -u postgres psql -X -p 5433 -d postgres -v ON_ERROR_STOP=1 <<'SQL'
\getenv app_user ISUREAL_DB_USER
\getenv app_password ISUREAL_DB_PASSWORD
SELECT format('CREATE ROLE %I LOGIN', :'app_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=:'app_user') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'app_user', :'app_password') \gexec
SELECT 'CREATE DATABASE isureal_assets' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='isureal_assets') \gexec
SQL
sudo --preserve-env=ISUREAL_DB_USER -u postgres psql -X -p 5433 -d isureal_assets -v ON_ERROR_STOP=1 <<'SQL'
\getenv app_user ISUREAL_DB_USER
CREATE TABLE IF NOT EXISTS astra_image_data (
    id varchar(36) PRIMARY KEY,
    data bytea NOT NULL CHECK (octet_length(data)>0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE astra_image_data ALTER COLUMN data SET STORAGE EXTERNAL;
SELECT format('GRANT CONNECT ON DATABASE isureal_assets TO %I', :'app_user') \gexec
SELECT format('GRANT USAGE ON SCHEMA public TO %I', :'app_user') \gexec
SELECT format('REVOKE ALL ON astra_image_data FROM %I', :'app_user') \gexec
SELECT format('GRANT SELECT, INSERT ON astra_image_data TO %I', :'app_user') \gexec
SELECT has_table_privilege(:'app_user','astra_image_data','SELECT') AS can_read,
       has_table_privilege(:'app_user','astra_image_data','INSERT') AS can_insert,
       has_table_privilege(:'app_user','astra_image_data','UPDATE') AS can_update,
       has_table_privilege(:'app_user','astra_image_data','DELETE') AS can_delete;
SELECT 1 / (has_table_privilege(:'app_user','astra_image_data','SELECT')
    AND has_table_privilege(:'app_user','astra_image_data','INSERT')
    AND NOT has_table_privilege(:'app_user','astra_image_data','UPDATE')
    AND NOT has_table_privilege(:'app_user','astra_image_data','DELETE'))::int AS immutable_permissions_verified;
SQL
systemctl is-active postgresql@16-astraassets
