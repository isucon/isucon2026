#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
read -r threads backlog image_cache_mib < <(python3 - <<'PYCONFIG'
import json
runtime=json.load(open('/home/isucon/astra-deploy/runtime.json'))
value=runtime['tokioWorkerThreads']
backlog=runtime['listenBacklog']
assert type(backlog) is int and 128 <= backlog <= 4096
assert type(value) is int and 1 <= value <= 16
image_cache_mib=runtime['imageCacheMiB']
assert type(image_cache_mib) is int and 64 <= image_cache_mib <= 1024
print(value,backlog,image_cache_mib)
PYCONFIG
)
mkdir -p "/etc/systemd/system/$service.service.d"
printf '[Service]\nEnvironment=TOKIO_WORKER_THREADS=%s\nEnvironment=ISUREAL_LISTEN_BACKLOG=%s\nEnvironment=ISUREAL_IMAGE_CACHE_MIB=%s\n' "$threads" "$backlog" "$image_cache_mib" > "/etc/systemd/system/$service.service.d/tokio.conf"
