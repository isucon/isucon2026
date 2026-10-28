#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
read -r threads backlog < <(python3 - <<'PYCONFIG'
import json
runtime=json.load(open('/home/isucon/astra-deploy/runtime.json'))
value=runtime['tokioWorkerThreads']
backlog=runtime['listenBacklog']
assert type(backlog) is int and 128 <= backlog <= 4096
assert type(value) is int and 1 <= value <= 16
print(value,backlog)
PYCONFIG
)
mkdir -p "/etc/systemd/system/$service.service.d"
printf '[Service]\nEnvironment=TOKIO_WORKER_THREADS=%s\nEnvironment=ISUREAL_LISTEN_BACKLOG=%s\n' "$threads" "$backlog" > "/etc/systemd/system/$service.service.d/tokio.conf"
