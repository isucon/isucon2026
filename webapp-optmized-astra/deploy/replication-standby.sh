#!/bin/bash
set -euo pipefail
replica=$1
case "$replica" in astra_europe|astra_america) ;; *) exit 2 ;; esac
data=/var/lib/postgresql/16/astra
bash "$(dirname "$0")/replica-credentials.sh"
ready=false
for attempt in $(seq 1 30); do
    if sudo -u postgres env PGPASSFILE=/run/astra-replication.pgpass psql --no-psqlrc --no-password \
        --dbname='host=192.168.148.10 port=5432 user=astra_repl dbname=replication replication=true' -c IDENTIFY_SYSTEM; then ready=true; break; fi
    sleep 2
done
[ "$ready" = true ] || { echo "Replication connection is not ready" >&2; exit 1; }
if [ ! -f "$data/.astra-basebackup-complete" ]; then
    if [ -d "$data" ]; then
        echo "Incomplete base backup at $data; inspect it before retrying" >&2
        exit 1
    fi
    systemctl stop postgresql@16-main
    install -d -m700 -o postgres -g postgres "$data"
    # Keep the original AMI database in 16/main; it is never removed.
    sudo -u postgres env PGPASSFILE=/run/astra-replication.pgpass /usr/lib/postgresql/16/bin/pg_basebackup \
        --host=192.168.148.10 --port=5432 --username=astra_repl --no-password \
        --pgdata="$data" --wal-method=stream --slot="$replica" --checkpoint=fast \
        --progress --verbose > /var/log/astra-basebackup.log 2>&1
    touch "$data/.astra-basebackup-complete"
    chown postgres:postgres "$data/.astra-basebackup-complete"
fi
cat >"$data/postgresql.auto.conf" <<CONF
# Managed by provisioning/aws/astra; the password comes from the shared private env file.
primary_conninfo = 'host=192.168.148.10 port=5432 user=astra_repl passfile=/run/astra-replication.pgpass application_name=$replica'
primary_slot_name = '$replica'
CONF
touch "$data/standby.signal"
chown postgres:postgres "$data/postgresql.auto.conf" "$data/standby.signal"
cat >/etc/postgresql/16/main/conf.d/astra-standby.conf <<'CONF'
data_directory = '/var/lib/postgresql/16/astra'
shared_buffers = '512MB'
effective_cache_size = '2GB'
max_connections = 200
hot_standby = on
hot_standby_feedback = on
max_standby_streaming_delay = '30s'
jit = off
CONF
systemctl enable postgresql
systemctl restart postgresql@16-main
sudo -u postgres psql -Atc 'SELECT pg_is_in_recovery(); SELECT status FROM pg_stat_wal_receiver;'
