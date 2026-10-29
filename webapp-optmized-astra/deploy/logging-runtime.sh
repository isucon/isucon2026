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
