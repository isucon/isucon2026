"""Aggregate existing Team 10 journals for a completed benchmark's load window.

Run from the repository root with a run ID. No log contents, IPs or usernames
are returned; only action counts and message byte totals are retained.
"""
import datetime
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

run = sys.argv[1]
assert re.fullmatch(r'\d{8}T\d{6}Z-team10', run)
log = (Path('var/aws-bench') / run / 'bench.log').read_text()
start = datetime.datetime.fromisoformat(re.search(r'time=([^ ]+) .*msg="phase: load"', log)[1])
end = start + datetime.timedelta(seconds=66)
program = 'since=' + repr(start.strftime('%Y-%m-%d %H:%M:%S UTC')) + '\nuntil=' + repr(end.strftime('%Y-%m-%d %H:%M:%S UTC')) + '\n' + r'''
import collections,json,subprocess
raw=subprocess.check_output(['journalctl','-u','webapp-optmized-astra','-u','webapp-optmized-astra-writer','--since',since,'--until',until,'-o','json'],text=True)
counts=collections.Counter();totals=collections.Counter()
for line in raw.splitlines():
 try:j=json.loads(line);row=json.loads(j['MESSAGE'])
 except (KeyError,ValueError):continue
 if not row.get('action'):continue
 counts[row['action']]+=1
 totals['action_rows']+=1
 totals['action_message_bytes']+=len(j['MESSAGE'].encode())+1
 if row.get('status',0)>=500 or (row['action']=='login' and row.get('status')==401):
  totals['retained_rows']+=1
  totals['retained_message_bytes']+=len(j['MESSAGE'].encode())+1
 if row['action']=='login' and row.get('status')==401:totals['failed_logins']+=1
print(json.dumps({'totals':dict(totals),'actions':dict(counts)}))
'''
applications = json.loads(Path('webapp-optmized-astra/deploy/runtime.json').read_text())['applications']

def inspect(app):
    raw = subprocess.check_output(['webapp-optmized-astra/deploy/ssh.sh', app['instanceId'], 'sudo python3 -c ' + shlex.quote(program)], text=True)
    return app['region'], json.loads(raw)

with ThreadPoolExecutor(max_workers=3) as pool:
    regions = dict(pool.map(inspect, applications))
print(json.dumps({'run':run,'since':start.isoformat(),'until':end.isoformat(),'regions':regions},indent=2))
