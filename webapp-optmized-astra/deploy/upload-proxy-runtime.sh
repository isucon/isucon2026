#!/bin/bash
set -euo pipefail
service=${1:?service required}
region=${2:?region required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
case "$region" in asia|europe|america) ;; *) exit 2 ;; esac
mkdir -p "/etc/systemd/system/$service.service.d"
python3 - "$(dirname "$0")/runtime.json" "$service" "$region" > "/etc/systemd/system/$service.service.d/upload-proxy.conf" <<'PY_CONFIG'
import json,sys
runtime=json.load(open(sys.argv[1]))
enabled=runtime.get('regionalUploadCacheEnabled',False) and sys.argv[2]=='webapp-optmized-astra' and sys.argv[3]!='asia'
if enabled:
    assert runtime['writeProxyEnabled'] and runtime['imageStore']['enabled'] and runtime['imageStore']['writesEnabled']
target='192.168.145.10:8081' if enabled else ''
regional=runtime.get('regionalImageWritesEnabled',False)
assert not regional or runtime.get('regionalImageStoresEnabled',False) and runtime.get('regionalUploadCacheEnabled',False)
print('[Service]')
print('Environment=ISUREAL_REGIONAL_IMAGE_WRITES='+str(int(regional)))
if regional:
    print('EnvironmentFile=/home/isucon/.astra-upload.env')
print('Environment=ISUREAL_UPLOAD_PROXY_TARGET='+target)
PY_CONFIG
