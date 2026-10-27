#!/bin/bash
set -euo pipefail
service=${1:?application service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
mkdir -p "/etc/systemd/system/$service.service.d"
python3 - "$(dirname "$0")/runtime.json" > "/etc/systemd/system/$service.service.d/image-store.conf" <<'PY_CONFIG'
import json,sys
s=json.load(open(sys.argv[1])).get('imageStore',{})
enabled=s.get('enabled',False)
assert not s.get('writesEnabled',False) or enabled
host=s['host'] if enabled else ''
port=s.get('port',5433)
writes=int(enabled and s.get('writesEnabled',False))
assert host in ('','192.168.145.10') and port==5433
print('[Service]')
print(f'Environment=ISUREAL_IMAGE_STORE_HOST={host}')
print(f'Environment=ISUREAL_IMAGE_STORE_PORT={port}')
print(f'Environment=ISUREAL_IMAGE_STORE_WRITES={writes}')
PY_CONFIG
