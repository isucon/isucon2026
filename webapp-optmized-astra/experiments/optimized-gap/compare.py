"""Compare existing official runs without running or modifying either app."""
import collections
import hashlib
import json
from pathlib import Path
import re

here = Path(__file__).resolve().parent
rust = here.parents[1] / 'results/20261029T131830Z-team12'


def read(directory):
    result = json.loads((directory / 'result.json').read_text())
    meta = json.loads((directory / 'meta.json').read_text())
    points = collections.Counter()
    for row in result['scores']:
        points[row['tag']] += row['points']
    assert sum(points.values()) == result['score']
    hosts = {}
    previous = None
    for line in (directory / 'report.txt').read_text().splitlines():
        match = re.match(r'^(bench|router|app[123]|db1)\s+(\d+)\s+([\d.]+)%\s+(\d+)%\s+(\d+)%', line)
        if match:
            name, cpus, average, p95, maximum = match.groups()
            hosts[name] = dict(cpus=int(cpus), cpu_average=float(average), cpu_p95=int(p95), cpu_max=int(maximum))
            wait = re.search(r'(\d+)/\s*(\d+)/\s*(\d+)/\s*([\d.]+)/\s*(\d+)', line)
            if wait:
                hosts[name]['iowait_average'] = int(wait[5])
        match = re.match(r'^\s+(router|app[123]|db1)\s+nvme', line)
        if match:
            previous = match[1]
        match = re.search(r'limit:.*; (\S+) baseline', line)
        if match and previous:
            hosts[previous]['instance_type'] = match[1]
    return dict(run=directory.name, score=result['score'], passed=result['pass'], language=result['language'],
                seed=result['seed'], benchmark_image=meta['image'], bench_args=meta['benchArgs'],
                runner_checkout_commit=meta['commit'], result_sha256=hashlib.sha256((directory / 'result.json').read_bytes()).hexdigest(),
                users=result['users'], points=dict(points), routes=result['routes'], hosts=hosts)


current = read(rust)
comparisons = []
for directory in sorted((here / 'benchmarks').iterdir()):
    reference = read(directory)
    assert reference['benchmark_image'] == current['benchmark_image']
    assert reference['seed'] == current['seed'] == 1
    gap = reference['score'] - current['score']
    tags = set(reference['points']) | set(current['points'])
    points = sorted([dict(tag=tag, go=reference['points'].get(tag, 0), rust=current['points'].get(tag, 0),
                         gap=reference['points'].get(tag, 0)-current['points'].get(tag, 0)) for tag in tags],
                    key=lambda row: row['gap'], reverse=True)
    assert sum(row['gap'] for row in points) == gap
    history_export = sum(row['gap'] for row in points if row['tag'] in ('history_month', 'history_date', 'export'))
    comparisons.append(dict(reference=reference, score_gap=gap, points=points,
                            history_export_gap=history_export, history_export_share=history_export/gap))
output = dict(rust=current, comparisons=comparisons,
              caveats=['Different VMs and execution times; this is not an isolated causal experiment.',
                       'runner_checkout_commit records the benchmark invocation checkout, not the deployed Go binary.',
                       'Route percentiles include transport, queuing and processing; they do not isolate SQL execution time.'])
(here / 'comparison.json').write_text(json.dumps(output, ensure_ascii=False, indent=2)+'\n')
for row in comparisons:
    print(row['reference']['run'], 'score', row['reference']['score'], 'gap', row['score_gap'],
          'history+export share', round(row['history_export_share']*100, 1))
