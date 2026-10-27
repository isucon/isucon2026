#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
threads=$(python3 - <<'PYCONFIG'
import json
value=json.load(open('/home/isucon/astra-deploy/runtime.json'))['tokioWorkerThreads']
assert type(value) is int and 1 <= value <= 16
print(value)
PYCONFIG
)
mkdir -p "/etc/systemd/system/$service.service.d"
printf '[Service]\nEnvironment=TOKIO_WORKER_THREADS=%s\n' "$threads" > "/etc/systemd/system/$service.service.d/tokio.conf"
