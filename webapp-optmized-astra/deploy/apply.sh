#!/bin/bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
export AWS_CONFIG_FILE="$root/provisioning/aws-sdk.config"
export AWS_SDK_LOAD_CONFIG=1
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state
pulumi up -C "$root/provisioning/aws/astra" --stack dev --yes
association=$(pulumi stack output deployment -C "$root/provisioning/aws/astra" --stack dev --json | jq -r .associationId)
# Pulumi may return before an updated SSM Association actually runs.
for attempt in $(seq 1 120); do
    status=$(aws ssm describe-association --association-id "$association" --query AssociationDescription.Overview.Status --output text)
    case "$status" in
        Success) echo "Deployment succeeded: $association"; exit 0 ;;
        Failed) echo "Deployment failed: $association" >&2; exit 1 ;;
    esac
    sleep 5
done
echo "Deployment observation timed out; inspect association $association before retrying" >&2
exit 1
