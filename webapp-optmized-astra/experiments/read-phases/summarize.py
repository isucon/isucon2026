"""Summarize identifier-free read timings within the benchmark load window."""
import argparse
import collections
import datetime
import json
import math
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('input', type=Path)
parser.add_argument('output', type=Path)
parser.add_argument('--since', required=True)
parser.add_argument('--until', required=True)
args = parser.parse_args()

def timestamp(value):
    date = datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
    assert date.tzinfo is not None
    return date.timestamp() * 1000

since, until = timestamp(args.since), timestamp(args.until)
assert since < until
source = json.loads(args.input.read_text())
output = {'since': args.since, 'until': args.until, 'regions': {},
          'caveat': 'One in eight memory requests, diagnostic logging enabled. Stage percentiles are not additive; absent stages are excluded.'}
for region, rows in source['regions'].items():
    routes = collections.defaultdict(list)
    for row in rows:
        if since <= row['at_ms'] < until:
            routes[row['route']].append(row)
    output['regions'][region] = {}
    for route, samples in routes.items():
        values = collections.defaultdict(list)
        for row in samples:
            values['total'].append(row['total_us'] / 1000)
            for stage, micros in row['stages_us'].items():
                values[stage].append(micros / 1000)
        summary = {}
        for stage, timings in values.items():
            timings.sort()
            summary[stage] = dict(samples=len(timings), mean=round(sum(timings)/len(timings), 3),
                p50=round(timings[math.ceil(len(timings)*0.5)-1], 3),
                p90=round(timings[math.ceil(len(timings)*0.9)-1], 3))
        output['regions'][region][route] = {'samples': len(samples), 'milliseconds': summary}
args.output.write_text(json.dumps(output, indent=2)+'\n')
print('Summarized', sum(s['samples'] for r in output['regions'].values() for s in r.values()), 'samples')
