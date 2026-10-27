#!/bin/bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
export AWS_CONFIG_FILE="$root/provisioning/aws-sdk.config"
export AWS_SDK_LOAD_CONFIG=1
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state
pulumi up -C "$root/provisioning/aws/astra" --stack dev --yes
associations=$(pulumi stack output -C "$root/provisioning/aws/astra" --stack dev --json | jq -r '.deployment.associationId, .databaseAssociationId // empty')
# Pulumi may return before an updated SSM Association actually runs.
for association in $associations; do
for attempt in $(seq 1 120); do
    status=$(aws ssm describe-association --association-id "$association" --query AssociationDescription.Overview.Status --output text)
    case "$status" in
        Success) echo "Deployment succeeded: $association"; break ;;
        Failed) echo "Deployment failed: $association" >&2; exit 1 ;;
    esac
    sleep 5
done
if [ "$status" != Success ]; then
echo "Deployment observation timed out; inspect association $association before retrying" >&2
exit 1
fi
done
