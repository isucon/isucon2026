#!/bin/bash
# Run serially; each completed score, including failures, is recorded under results/.
# Team 12: commit the recorded evidence manually; this script never pushes.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$root/var/astra"
exec 9>"$root/var/astra/operation.lock"
flock -n 9 || { echo 'Another Astra deployment, maintenance operation, or benchmark is active' >&2; exit 1; }
cd "$root"
python3 webapp-optmized-astra/deploy/check-benchmark-space.py
label=${1:?experiment label required}
image=${IMAGE:-124502390568.dkr.ecr.ap-northeast-1.amazonaws.com/isucon2026-bench-runner-dev@sha256:55649c0a80a6c52e8add443356e24d794f47086d646a34828924595190cfb26d}
previous=$(tail -n1 var/aws-bench/runs.tsv 2>/dev/null || true)
status=0
make aws-bench-run TEAM=12 IMAGE="$image" BENCH_ARGS="${BENCH_ARGS:-}" || status=$?
latest=$(tail -n1 var/aws-bench/runs.tsv)
if [ "$previous" = "$latest" ]; then
    echo 'No new completed result; inspect the existing task before retrying.' >&2
    exit 1
fi
run=$(printf '%s' "$latest" | cut -f1)
team=$(printf '%s' "$latest" | cut -f2)
[ "$team" = 12 ] || { echo 'Unexpected team in latest result' >&2; exit 1; }
python3 webapp-optmized-astra/deploy/record.py "$run" "$label"
score=$(jq -r .score "var/aws-bench/$run/result.json")
passed=$(jq -r .pass "var/aws-bench/$run/result.json")
echo "$run score=$score pass=$passed"
exit "$status"
