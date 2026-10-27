#!/bin/bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
export AWS_CONFIG_FILE="$root/provisioning/aws-sdk.config"
export AWS_SDK_LOAD_CONFIG=1
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state
# This stack contains runtime configuration only; it creates no AWS resources.
pulumi up -C "$root/provisioning/aws/astra" --stack dev --yes
python3 "$root/webapp-optmized-astra/deploy/ssh-apply.py" "${1:-all}"
