"""Read-only 500 ms activity samples on Team 12 primary and metadata replicas.

Run during a separately started benchmark. This does not reset statistics,
enable extensions, change server settings, or record SQL parameter values.
"""
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys

root = Path(__file__).resolve().parents[3]
runtime = json.loads((root / 'webapp-optmized-astra/deploy/runtime.json').read_text())
output = Path(sys.argv[1])
seconds = int(sys.argv[2])
assert 1 <= seconds <= 300
env = dict(os.environ, AWS_CONFIG_FILE=str(root / 'provisioning/aws-sdk.config'), AWS_SDK_LOAD_CONFIG='1')
targets = [('primary', runtime['database']['instanceId'])]
targets += [(app['region'], app['instanceId']) for app in runtime['applications'] if app.get('replicaName')]
query = """SELECT json_build_object('observed_at',extract(epoch FROM clock_timestamp()),'queries',
coalesce(json_agg(row_to_json(s)), '[]'::json)) FROM (
SELECT backend_type, state, left(regexp_replace(query,$q$'[^']*'$q$,'?','g'),1200) AS query,
coalesce(wait_event_type,'CPU') AS wait_type, coalesce(wait_event,'none') AS wait_event,
count(*) AS connections, max(extract(epoch FROM clock_timestamp()-query_start)) AS age_seconds
FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND
((datname=current_database() AND usename='isureal' AND state IN ('active','idle in transaction'))
OR backend_type='startup') GROUP BY 1,2,3,4,5) s"""


def sample(target):
    name, instance = target
    command = 'printf %s '+shlex.quote(query+';\n\\watch 0.5\n')+' | timeout '+str(seconds)+' sudo -u postgres psql -XAt -d isureal'
    result = subprocess.run([str(root/'webapp-optmized-astra/deploy/ssh.sh'), instance, command],
                            env=env, text=True, capture_output=True)
    if result.returncode not in (0, 124):
        raise RuntimeError(name+': '+result.stderr)
    rows = [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]
    assert rows, name
    print(name, len(rows), 'samples', flush=True)
    return name, rows


with ThreadPoolExecutor(max_workers=3) as pool:
    samples = dict(pool.map(sample, targets))
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(dict(interval_seconds=0.5, hosts=samples), ensure_ascii=False, indent=2)+'\n')
