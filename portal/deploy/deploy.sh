#!/bin/sh
# Shared local/CI sequence. ecspresso owns registration, updates and waiters.
set -eu
: "${PORTAL_CONFIG_URI:?Set PORTAL_CONFIG_URI from Pulumi ecspressoConfigUri output}"
: "${IMAGE_DIGEST:?Set IMAGE_DIGEST to an existing ECR sha256 digest}"
if ! printf '%s\n' "$IMAGE_DIGEST" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
  echo 'IMAGE_DIGEST must be an immutable sha256 digest' >&2
  exit 1
fi
export IMAGE_DIGEST
cd "$(dirname "$0")/ecspresso"
aws s3 cp "$PORTAL_CONFIG_URI" infra.json
SERVICE=migrate ecspresso --config ecspresso.jsonnet run --wait --watch-container migrate
for service in portal worker notifyloop bot; do
  SERVICE="$service" ecspresso --config ecspresso.jsonnet deploy --wait --wait-until deployed
done
