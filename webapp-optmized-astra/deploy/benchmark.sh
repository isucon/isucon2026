#!/bin/bash
# Run serially; each completed score, including failures, is committed and pushed.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$root/var/astra"
exec 9>"$root/var/astra/operation.lock"
flock -n 9 || { echo 'Another Astra deployment, maintenance operation, or benchmark is active' >&2; exit 1; }
cd "$root"
label=${1:?experiment label required}
image=${IMAGE:-124502390568.dkr.ecr.ap-northeast-1.amazonaws.com/isucon2026-bench-runner-dev@sha256:32b75899cbb6cf373a95889606b225b57dbf5e05d102fcf949bfaec657d83454}
previous=$(tail -n1 var/aws-bench/runs.tsv 2>/dev/null || true)
status=0
make aws-bench-run TEAM=10 IMAGE="$image" BENCH_ARGS="${BENCH_ARGS:-}" || status=$?
latest=$(tail -n1 var/aws-bench/runs.tsv)
if [ "$previous" = "$latest" ]; then
    echo 'No new completed result; inspect the existing task before retrying.' >&2
    exit 1
fi
run=$(printf '%s' "$latest" | cut -f1)
team=$(printf '%s' "$latest" | cut -f2)
[ "$team" = 10 ] || { echo 'Unexpected team in latest result' >&2; exit 1; }
python3 webapp-optmized-astra/deploy/record.py "$run" "$label"
score=$(jq -r .score "var/aws-bench/$run/result.json")
passed=$(jq -r .pass "var/aws-bench/$run/result.json")
git add -- webapp-optmized-astra provisioning/aws/astra
git diff --cached --check
git commit -m "perf(astra): $label; score $score pass=$passed ($run)"
git push origin HEAD:webapp-optmized-astra
exit "$status"
