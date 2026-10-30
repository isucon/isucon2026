"""Read-only headroom check; benchmark.sh holds the operation lock.

Repeated initialize removes SQL references but deliberately leaves immutable
files for the existing offline GC. Do not start a run on a nearly full VM.
"""
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess

root=Path(__file__).resolve().parents[2]
deploy=root/'webapp-optmized-astra/deploy'
runtime=json.loads((deploy/'runtime.json').read_text())
env=dict(os.environ,AWS_CONFIG_FILE=str(root/'provisioning/aws-sdk.config'),AWS_SDK_LOAD_CONFIG='1')
targets=[(a['region'],a['instanceId'],4) for a in runtime['applications']]
targets.append(('primary',runtime['database']['instanceId'],2))

def inspect(target):
    name,instance,minimum_gib=target
    output=subprocess.check_output([str(deploy/'ssh.sh'),instance,
        'df -B1 --output=avail / | tail -n1'],env=env,text=True,timeout=45)
    available=int(output.strip())
    return dict(host=name,available_bytes=available,minimum_bytes=minimum_gib*1024**3)

with ThreadPoolExecutor(max_workers=4) as pool:
    rows=list(pool.map(inspect,targets))
for row in rows:
    print(f"{row['host']}: free={row['available_bytes']/1024**3:.1f} GiB; required={row['minimum_bytes']/1024**3:.0f} GiB",flush=True)
record=dict(observed_at=datetime.now(timezone.utc).isoformat(),hosts=rows)
(root/'var/astra/space-before-benchmark.json').write_text(json.dumps(record,indent=2)+'\n')
if any(row['available_bytes']<row['minimum_bytes'] for row in rows):
    raise SystemExit('Insufficient space before benchmark. Inspect storage and run apply.sh image-gc for unreferenced assets, then verify runtime before retrying.')
