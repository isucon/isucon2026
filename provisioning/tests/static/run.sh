#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

mise exec -- python -m unittest discover \
  -s "${repo_root}/provisioning/tests/static" \
  -p 'test_*.py' \
  -v

jq empty "${repo_root}"/specs/004-local-vm-provisioning/contracts/*.json

PORTAL_SERVER_BIND_IP=127.0.0.1 \
PORTAL_SERVER_PORT=18082 \
PORTAL_WEB_BIND_IP=127.0.0.1 \
PORTAL_WEB_PORT=15174 \
  docker compose -f "${repo_root}/portal-mock/compose.yaml" config --quiet
