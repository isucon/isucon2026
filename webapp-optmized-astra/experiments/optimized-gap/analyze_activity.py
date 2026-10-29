"""Summarize sampled connection occupancy, not query counts or CPU time."""
from collections import Counter
from datetime import datetime
import gzip
import hashlib
import json
from pathlib import Path
import re

here = Path(__file__).resolve().parent
profile = here / 'activity-20261029T150301Z-team12.json.gz'
window = json.loads((here / 'activity-window.json').read_text())
start, end = (datetime.fromisoformat(window[key]).timestamp() for key in ('load_start', 'final_check_start'))
samples = json.loads(gzip.decompress(profile.read_bytes()))


def shape(query):
    # Collapse variable-sized prepared IN lists while preserving other SQL.
    return re.sub(r'\$\d+(?:\s*,\s*\$\d+)+', '$list', ' '.join(query.split()))


def summarize(rows):
    waits, queries, states, totals = Counter(), Counter(), Counter(), Counter()
    ages, query_waits = {}, {}
    client_max = sync_max = 0
    for row in rows:
        client = sync = 0
        for q in row['queries']:
            n = q['connections']
            backend = q['backend_type']
            totals[backend] += n
            states[(backend, q['state'])] += n
            waits[(backend, q['wait_type'], q['wait_event'])] += n
            key = backend, shape(q['query'])
            queries[key] += n
            ages[key] = max(ages.get(key, 0), q['age_seconds'] or 0)
            query_waits.setdefault(key, Counter())[(q['wait_type'], q['wait_event'])] += n
            if backend == 'client backend':
                client += n
                if q['wait_event'] == 'SyncRep':
                    sync += n
        client_max = max(client_max, client)
        sync_max = max(sync_max, sync)
    return dict(
        frames=len(rows), first_sample=rows[0]['observed_at'], last_sample=rows[-1]['observed_at'],
        connection_samples=dict(totals),
        average_observed_clients=totals['client backend']/len(rows),
        maximum_observed_clients=client_max, maximum_sync_rep_clients=sync_max,
        states=[dict(backend_type=k[0], state=k[1], connection_samples=n) for k, n in states.most_common()],
        waits=[dict(backend_type=k[0], wait_type=k[1], wait_event=k[2], connection_samples=n)
               for k, n in waits.most_common()],
        queries=[dict(backend_type=k[0], query=k[1], connection_samples=n,
                      maximum_sampled_query_age_seconds=ages[k],
                      waits=[dict(wait_type=w[0], wait_event=w[1], connection_samples=c)
                             for w, c in query_waits[k].most_common()])
                 for k, n in queries.most_common()])


hosts = {}
for host, frames in samples['hosts'].items():
    assert frames[0]['observed_at'] < start and frames[-1]['observed_at'] > end, host
    selected = [r for r in frames if start <= r['observed_at'] < end]
    assert len(selected) >= int((end-start)/0.5)-2, host
    hosts[host] = summarize(selected)
    hosts[host]['last_30_seconds'] = summarize([r for r in selected if r['observed_at'] >= end-30])
result = dict(
    window=window, profile_sha256=hashlib.sha256(profile.read_bytes()).hexdigest(),
    interval_seconds=samples['interval_seconds'], hosts=hosts,
    caveats=[
        'Counts are connection samples at 500 ms intervals, not completed SQL calls.',
        'Client rows include active and idle-in-transaction states; states are reported separately.',
        'CPU/none means no reported wait event; it does not measure CPU execution time.',
        'Query age is sampled time since query_start, not request latency or a complete latency distribution.',
        'SQL text may be truncated by PostgreSQL; prepared parameter values were not collected.',
        'Application pool/semaphore queues and individual WAL replay CPU consumption are not measured.',
        'Profiling overhead and ordinary benchmark variation are not separated by this single run.',
    ])
(here / 'activity-summary.json').write_text(json.dumps(result, ensure_ascii=False, indent=2)+'\n')
for name, data in hosts.items():
    print(name, 'frames', data['frames'], 'connection samples', data['connection_samples'],
          'max SyncRep', data['maximum_sync_rep_clients'])
