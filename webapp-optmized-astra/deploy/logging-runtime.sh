#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
python3 - "$(dirname "$0")/runtime.json" > /etc/astra-application-log.env <<'PY_CONFIG'
import json,sys
runtime=json.load(open(sys.argv[1]))
level=runtime['applicationLogLevel']
assert level in ('debug','info'), 'Failed-login info logs are required by the existing guard'
print('ISUREAL_LOG_LEVEL='+level)
for key,env in [('signatureCacheCoalescing','ISUREAL_SIGNATURE_COALESCE'),('signatureCacheMetrics','ISUREAL_SIGNATURE_METRICS')]:
    assert type(runtime[key]) is bool
    print(env+'='+str(int(runtime[key])))
PY_CONFIG
chmod 644 /etc/astra-application-log.env
mkdir -p "/etc/systemd/system/$service.service.d"
printf '[Service]\nEnvironmentFile=/etc/astra-application-log.env\n' > "/etc/systemd/system/$service.service.d/logging.conf"
