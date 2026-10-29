#!/bin/bash
set -euo pipefail
mkdir -p /usr/local/lib/astra
cat >/usr/local/lib/astra/replica-credentials <<'SCRIPT'
#!/bin/bash
set -euo pipefail
umask 077
. /home/isucon/.astra-replication.env
password=$ASTRA_REPL_PASSWORD
printf '192.168.180.10:5432:replication:astra_repl:%s\n' "$password" >/run/astra-replication.pgpass
chown postgres:postgres /run/astra-replication.pgpass
chmod 600 /run/astra-replication.pgpass
SCRIPT
chmod 755 /usr/local/lib/astra/replica-credentials
rm -f /etc/astra-replication-secret-arn
cat >/etc/systemd/system/astra-replica-credentials.service <<'UNIT'
[Unit]
Description=Load Astra replication credentials from the shared private env file
After=network-online.target
Wants=network-online.target
Before=postgresql@16-main.service

[Service]
Type=oneshot
ExecStart=/usr/local/lib/astra/replica-credentials
RemainAfterExit=yes
UNIT
mkdir -p /etc/systemd/system/postgresql@16-main.service.d
cat >/etc/systemd/system/postgresql@16-main.service.d/astra.conf <<'UNIT'
[Unit]
Requires=astra-replica-credentials.service
After=astra-replica-credentials.service
UNIT
systemctl daemon-reload
refreshed=false
for attempt in $(seq 1 30); do
    # Restarting this required unit also stops PostgreSQL. Refresh the passfile
    # directly when the boot-time oneshot is already active.
    if systemctl is-active --quiet astra-replica-credentials; then
        if /usr/local/lib/astra/replica-credentials; then refreshed=true; break; fi
    elif systemctl start astra-replica-credentials; then
        refreshed=true
        break
    fi
    sleep 2
done
[ "$refreshed" = true ] || { echo 'Replication credentials could not be refreshed' >&2; exit 1; }
systemctl is-active astra-replica-credentials
