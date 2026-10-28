#!/bin/bash
set -euo pipefail
service=${1:?application service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
mkdir -p "/etc/systemd/system/$service.service.d"
python3 - "$(dirname "$0")/runtime.json" > "/etc/systemd/system/$service.service.d/image-store.conf" <<'PY_CONFIG'
import json,sys
runtime=json.load(open(sys.argv[1]))
s=runtime.get('imageStore',{})
enabled=s.get('enabled',False)
assert not s.get('writesEnabled',False) or enabled
host=s['host'] if enabled else ''
port=s.get('port',5433)
writes=int(enabled and s.get('writesEnabled',False))
assert host in ('','192.168.145.10') and port==5433
regions={app['region']:app['host'] for app in runtime['applications'] if app['region']!='asia'} if runtime.get('regionalImageStoresEnabled',False) else {}
assert not regions or enabled
print('[Service]')
print("Environment='ISUREAL_REGIONAL_IMAGE_HOSTS_JSON="+json.dumps(regions,separators=(',',':'))+"'")
print(f'Environment=ISUREAL_IMAGE_STORE_HOST={host}')
print(f'Environment=ISUREAL_IMAGE_STORE_PORT={port}')
print(f'Environment=ISUREAL_IMAGE_STORE_WRITES={writes}')
print(f"Environment=ISUREAL_IMAGE_STORE_CACHE={int(enabled and s.get('cacheEnabled',False))}")
cache_enabled=enabled and runtime.get('imageCachePushEnabled',False)
peers={app['region']: app['host']+':8080' for app in runtime['applications'] if app['region']!='asia'} if cache_enabled else None
if peers is None and enabled and runtime.get('localUploadCacheEnabled',False):
    peers={}
peer_json=json.dumps(peers,separators=(',',':')) if peers is not None else ''
source=next(app['host'] for app in runtime['applications'] if app['region']=='asia') if cache_enabled else ''
print(f"Environment='ISUREAL_IMAGE_CACHE_PEERS_JSON={peer_json}'")
print(f'Environment=ISUREAL_IMAGE_CACHE_SOURCE_HOST={source}')
PY_CONFIG
