#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
python3 - "$(dirname "$0")/runtime.json" > /etc/astra-application-log.env <<'PY_CONFIG'
import json,sys
runtime=json.load(open(sys.argv[1]))
level=runtime['applicationLogLevel']
assert level in ('debug','info','error'), 'Invalid application log level'
# Failed-login action logs bypass this diagnostic threshold in the application.
print('ISUREAL_LOG_LEVEL='+level)
for key,env in [('signatureCacheCoalescing','ISUREAL_SIGNATURE_COALESCE'),('signatureCacheMetrics','ISUREAL_SIGNATURE_METRICS'),('issuedImageUrlCacheEnabled','ISUREAL_ISSUED_URL_CACHE')]:
    assert type(runtime[key]) is bool
    print(env+'='+str(int(runtime[key])))
auth_timing=runtime.get('authTimingEnabled',False)
assert type(auth_timing) is bool
print('ISUREAL_AUTH_TIMING='+str(int(auth_timing)))
post_timing=runtime.get('postTimingEnabled',False)
assert type(post_timing) is bool
print('ISUREAL_POST_TIMING='+str(int(post_timing)))
read_timing=runtime.get('readTimingEnabled',False)
assert type(read_timing) is bool
print('ISUREAL_READ_TIMING='+str(int(read_timing)))
history=runtime.get('initialHistoryEnabled',False)
assert type(history) is bool
print('ISUREAL_INITIAL_HISTORY='+str(int(history)))
history_export=runtime.get('initialHistoryExportEnabled',False)
assert type(history_export) is bool
print('ISUREAL_INITIAL_HISTORY_EXPORT='+str(int(history_export)))
initial_images=runtime.get('initialPostImagesEnabled',False)
assert type(initial_images) is bool
print('ISUREAL_INITIAL_POST_IMAGES='+str(int(initial_images)))
initial_image_pool=runtime.get('initialImagePoolConnections',0)
assert type(initial_image_pool) is int and 0 <= initial_image_pool <= 16
print('ISUREAL_INITIAL_IMAGE_POOL_CONNECTIONS='+str(initial_image_pool))
memory_pool=runtime.get('memoryPoolConnections',0)
assert type(memory_pool) is int and 0 <= memory_pool <= 16
print('ISUREAL_MEMORY_POOL_CONNECTIONS='+str(memory_pool))
short_pool=runtime.get('shortReadPoolConnections',0)
assert type(short_pool) is int and 0 <= short_pool <= 16
print('ISUREAL_SHORT_READ_POOL_CONNECTIONS='+str(short_pool))
recommendation_region=runtime.get('recommendationReadRegion','')
assert recommendation_region in ('','primary','europe','america')
recommendation_host=runtime['database']['host'] if recommendation_region=='primary' else next((a['host'] for a in runtime['applications'] if a['region']==recommendation_region),'')
print('ISUREAL_RECOMMENDATION_DB_HOST='+recommendation_host)
print('ISUREAL_RECOMMENDATION_READ_REGION='+recommendation_region)
recommendation_pool=runtime.get('recommendationPoolConnections',32)
assert type(recommendation_pool) is int and 4 <= recommendation_pool <= 64
print('ISUREAL_RECOMMENDATION_POOL_CONNECTIONS='+str(recommendation_pool))
session_pool=runtime.get('sessionPoolConnections',0)
assert type(session_pool) is int and 0 <= session_pool <= 16
print('ISUREAL_SESSION_POOL_CONNECTIONS='+str(session_pool))
graph=runtime.get('initialGraphEnabled',False)
assert type(graph) is bool
print('ISUREAL_INITIAL_GRAPH='+str(int(graph)))
for key,env in [('primaryAsiaSessionsEnabled','ISUREAL_PRIMARY_ASIA_SESSIONS'),('localAsiaSessionWritesEnabled','ISUREAL_LOCAL_ASIA_SESSION_WRITES')]:
    assert type(runtime.get(key,False)) is bool
    print(env+'='+str(int(runtime.get(key,False))))
session_cache=runtime.get('sessionCacheEnabled',False)
assert type(session_cache) is bool
print('ISUREAL_SESSION_CACHE='+str(int(session_cache)))
print("ISUREAL_SESSION_CACHE_PEERS='"+json.dumps([app['host']+':8080' for app in runtime['applications']],separators=(',',':'))+"'")
entries=runtime['issuedImageUrlCacheEntries']
assert type(entries) is int and 16 <= entries <= 262144 and entries % 16 == 0
print('ISUREAL_ISSUED_URL_CACHE_ENTRIES='+str(entries))
PY_CONFIG
chmod 644 /etc/astra-application-log.env
mkdir -p "/etc/systemd/system/$service.service.d"
python3 - "$(dirname "$0")/runtime.json" "$service" > "/etc/systemd/system/$service.service.d/signature-file.conf" <<'PY_SIGNATURE'
import json,sys
runtime=json.load(open(sys.argv[1]))
path='/home/isucon/.astra-signatures.bin' if runtime['signatureFileCacheEnabled'] and sys.argv[2]=='webapp-optmized-astra' else ''
print('[Service]\nEnvironment=ISUREAL_SIGNATURE_FILE='+path)
PY_SIGNATURE
printf '[Service]\nEnvironmentFile=/etc/astra-application-log.env\n' > "/etc/systemd/system/$service.service.d/logging.conf"
