"""Collect opt-in memory read timings without request identifiers."""
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
stages = {'auth','pool_wait','posts_sql','posts_render','reactions_sql','reactions_render','comments_sql','comments_render','json'}
result = []
for line in rows.splitlines():
    try:
        journal = json.loads(line)
        row = json.loads(journal['MESSAGE'])
    except (ValueError, KeyError):
        continue
    if row.get('event') != 'memory_read_timing':
        continue
    assert row['route'] in ('month','day')
    assert all(type(row[k]) is int and row[k] >= 0 for k in ('at_ms','posts','total_us'))
    assert set(row['stages_us']) <= stages
    assert all(type(v) is int and v >= 0 for v in row['stages_us'].values())
    result.append(dict(service=journal['_SYSTEMD_UNIT'], **{k:row[k] for k in ('route','at_ms','posts','total_us','stages_us')}))
print(json.dumps(result))
'''
output = {'since': since, 'until': until, 'regions': {}}
for app in runtime['applications']:
    remote = 'SINCE=' + repr(since) + '\nUNTIL=' + repr(until) + '\n' + program
    rows = subprocess.check_output(['webapp-optmized-astra/deploy/ssh.sh', app['instanceId'], 'sudo python3 -c ' + shlex.quote(remote)], text=True)
    output['regions'][app['region']] = json.loads(rows)
args.output.write_text(json.dumps(output, indent=2) + '\n')
print('Collected', sum(map(len, output['regions'].values())), 'memory read timings')
