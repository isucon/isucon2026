#!/bin/bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$root/var/astra"
exec 9>"$root/var/astra/operation.lock"
flock -n 9 || { echo 'Another Astra deployment, maintenance operation, or benchmark is active' >&2; exit 1; }
export AWS_CONFIG_FILE="$root/provisioning/aws-sdk.config"
export AWS_SDK_LOAD_CONFIG=1
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state
# This stack contains runtime configuration only; it creates no AWS resources.
pulumi up -C "$root/provisioning/aws/astra" --stack dev --yes
python3 "$root/webapp-optmized-astra/deploy/ssh-apply.py" "${1:-all}"
