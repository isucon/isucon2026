#!/bin/bash
set -euo pipefail
synchronous=$1
case "$synchronous" in true|false) ;; *) exit 2 ;; esac
if [ "$synchronous" = false ]; then
    # Permit rollback when a previously synchronous standby is unavailable.
    cat >/etc/postgresql/16/main/conf.d/astra-replication.conf <<'CONF'
wal_keep_size = '2GB'
max_slot_wal_keep_size = '4GB'
synchronous_standby_names = ''
synchronous_commit = on
CONF
    sudo -u postgres psql -Atc 'SELECT pg_reload_conf()'
fi
set -a
. /home/isucon/.astra-replication.env
set +a
sudo --preserve-env=ASTRA_REPL_PASSWORD -u postgres psql --no-psqlrc --set ON_ERROR_STOP=1 <<'SQL'
\getenv replication_password ASTRA_REPL_PASSWORD
SELECT 'CREATE ROLE astra_repl LOGIN REPLICATION' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'astra_repl') \gexec
ALTER ROLE astra_repl WITH LOGIN REPLICATION PASSWORD :'replication_password';
SELECT pg_create_physical_replication_slot('astra_europe') WHERE NOT EXISTS (SELECT FROM pg_replication_slots WHERE slot_name = 'astra_europe');
SELECT pg_create_physical_replication_slot('astra_america') WHERE NOT EXISTS (SELECT FROM pg_replication_slots WHERE slot_name = 'astra_america');
SQL
unset ASTRA_REPL_PASSWORD
for ip in 192.168.146.10 192.168.147.10; do
    rule="host replication astra_repl $ip/32 scram-sha-256"
    grep -qxF "$rule" /etc/postgresql/16/main/pg_hba.conf || printf '%s\n' "$rule" >>/etc/postgresql/16/main/pg_hba.conf
done
cat >/etc/postgresql/16/main/conf.d/astra-replication.conf <<'CONF'
wal_keep_size = '2GB'
max_slot_wal_keep_size = '4GB'
CONF
if [ "$synchronous" = true ]; then
    count=$(sudo -u postgres psql -Atc "SELECT count(*) FROM pg_stat_replication WHERE application_name IN ('astra_europe','astra_america') AND state='streaming'")
    [ "$count" = 2 ] || { echo "Both replicas must be streaming before synchronous activation" >&2; exit 1; }
    cat >>/etc/postgresql/16/main/conf.d/astra-replication.conf <<'CONF'
synchronous_standby_names = 'FIRST 2 (astra_europe, astra_america)'
synchronous_commit = remote_apply
CONF
else
    cat >>/etc/postgresql/16/main/conf.d/astra-replication.conf <<'CONF'
synchronous_standby_names = ''
synchronous_commit = on
CONF
fi
sudo -u postgres psql -Atc 'SELECT pg_reload_conf()'
echo "Primary replication configuration installed; synchronous=$synchronous"
