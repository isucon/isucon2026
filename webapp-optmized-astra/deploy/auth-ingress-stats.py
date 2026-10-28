"""Collect opt-in Nginx auth timings without credentials or user identifiers."""
import argparse
import datetime
import json
from pathlib import Path
import shlex
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('since', help='UTC ISO timestamp')
parser.add_argument('until', help='UTC ISO timestamp')
parser.add_argument('output', type=Path)
args = parser.parse_args()

def utc(value):
    timestamp = datetime.datetime.fromisoformat(value)
    if timestamp.tzinfo is None:
        timestamp = timestamp.replace(tzinfo=datetime.timezone.utc)
    return timestamp.astimezone(datetime.timezone.utc)

since, until = utc(args.since), utc(args.until)
assert since < until
runtime = json.loads(Path('webapp-optmized-astra/deploy/runtime.json').read_text())
program = r'''
import json, math, re
from pathlib import Path
path = Path('/var/log/nginx/astra-auth-timing.log')
result = []
if path.exists():
    for line in path.open():
        row = json.loads(line)
        assert row['metric'] == 'auth_ingress'
        assert type(row['at_s']) in (int, float) and math.isfinite(row['at_s'])
        if not SINCE <= row['at_s'] <= UNTIL:
            continue
        assert row['action'] in ('register', 'login')
        assert type(row['status']) is int and 100 <= row['status'] <= 599
        assert type(row['request_s']) in (int, float) and row['request_s'] >= 0
        assert row['complete'] in ('', 'OK')
        for key in ('connect_s', 'header_s', 'upstream_s'):
            assert isinstance(row[key], str) and re.fullmatch(r'[-0-9.,: ]+', row[key])
        for key in ('request_bytes', 'response_bytes'):
            assert type(row[key]) is int and row[key] >= 0
        keys = ['at_s', 'action', 'status', 'request_s', 'connect_s', 'header_s',
                'upstream_s', 'request_bytes', 'response_bytes', 'complete']
        result.append({key: row[key] for key in keys})
print(json.dumps(result))
'''
output = {'since': since.isoformat(), 'until': until.isoformat(), 'regions': {}}
for app in runtime['applications']:
    remote = 'SINCE=' + repr(since.timestamp()) + '\nUNTIL=' + repr(until.timestamp()) + '\n' + program
    rows = subprocess.check_output(['webapp-optmized-astra/deploy/ssh.sh', app['instanceId'],
                                   'sudo python3 -c ' + shlex.quote(remote)], text=True)
    output['regions'][app['region']] = json.loads(rows)
args.output.write_text(json.dumps(output, indent=2) + '\n')
print('Collected', sum(map(len, output['regions'].values())), 'auth ingress timings')
