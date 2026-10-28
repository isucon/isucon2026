#!/bin/bash
# Called only on the existing Asia app VM after readers return to the primary.
set -euo pipefail
python3 - <<'PY'
import json,subprocess
runtime=json.load(open('/home/isucon/astra-deploy/runtime.json'))
assert not runtime['asiaReplicaEnabled'] and not runtime['asiaReplicaReads']
for service in ['webapp-optmized-astra','webapp-optmized-astra-writer']:
    pid=subprocess.check_output(['systemctl','show',service,'-p','MainPID','--value'],text=True).strip()
    assert int(pid)>0
    values=dict(entry.split(b'=',1) for entry in open('/proc/'+pid+'/environ','rb').read().split(b'\0') if b'=' in entry)
    assert values.get(b'ISUREAL_DB_READ_HOST',b'')==b'', 'Deploy primary readers before stopping the Asia standby'
PY
# Original AMI databases are retained. Only our dedicated derived standby stops.
if [ -f /var/lib/postgresql/16/astra/.astra-basebackup-complete ]; then
    if systemctl is-active --quiet postgresql@16-main; then
        state=$(sudo -u postgres psql -XAtc "SELECT current_setting('data_directory'), pg_is_in_recovery(), current_setting('primary_slot_name')")
        [ "$state" = '/var/lib/postgresql/16/astra|t|astra_asia' ] || { echo 'Unexpected Asia metadata cluster' >&2; exit 1; }
    fi
    systemctl stop postgresql@16-main
    echo manual > /etc/postgresql/16/main/start.conf
fi
