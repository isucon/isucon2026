"""Collect opt-in post timings without request identifiers."""
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
    return datetime.datetime.fromisoformat(value).replace(tzinfo=datetime.timezone.utc).isoformat()

since, until = utc(args.since), utc(args.until)
assert since < until
runtime = json.loads(Path('webapp-optmized-astra/deploy/runtime.json').read_text())
program = r'''
import json, subprocess
rows = subprocess.check_output(['journalctl', '-u', 'webapp-optmized-astra', '-u', 'webapp-optmized-astra-writer', '--since', SINCE, '--until', UNTIL, '-o', 'json'], text=True)
fields = {'slow_post':['at_ms','status','admission_us','handler_us','fence_us'], 'post_work':['at_ms','stored','validation_us','store_us','sql_us','response_us']}
result = []
for line in rows.splitlines():
    try:
        journal = json.loads(line)
        row = json.loads(journal['MESSAGE'])
    except (ValueError, KeyError):
        continue
    if row.get('metric') not in fields:
        continue
    keys = fields[row['metric']]
    assert all(type(row[k]) is int and row[k] >= 0 for k in keys)
    result.append(dict(service=journal['_SYSTEMD_UNIT'], metric=row['metric'], **{k: row[k] for k in keys}))
print(json.dumps(result))
'''
output = {'since': since, 'until': until, 'regions': {}}
for app in runtime['applications']:
    remote = 'SINCE=' + repr(since) + '\nUNTIL=' + repr(until) + '\n' + program
    rows = subprocess.check_output(['webapp-optmized-astra/deploy/ssh.sh', app['instanceId'], 'sudo python3 -c ' + shlex.quote(remote)], text=True)
    output['regions'][app['region']] = json.loads(rows)
args.output.write_text(json.dumps(output, indent=2) + '\n')
print('Collected', sum(map(len, output['regions'].values())), 'post timings')
