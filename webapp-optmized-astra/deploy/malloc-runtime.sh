#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
mkdir -p "/etc/systemd/system/$service.service.d"
python3 - "$(dirname "$0")/runtime.json" "$service" <<'PY_CONFIG'
import json,sys
from pathlib import Path
runtime=json.loads(Path(sys.argv[1]).read_text())
threshold=runtime.get('applicationMallocThresholdKiB',0)
assert type(threshold) is int and threshold in (0,64,128,256,512)
keys=['MALLOC_MMAP_THRESHOLD_','MALLOC_TRIM_THRESHOLD_']
if threshold:
    text='[Service]\n'+''.join('Environment='+key+'='+str(threshold*1024)+'\n' for key in keys)
else:
    text='[Service]\nUnsetEnvironment='+' '.join(keys)+'\n'
Path('/etc/systemd/system/'+sys.argv[2]+'.service.d/malloc.conf').write_text(text)
PY_CONFIG
