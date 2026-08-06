#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
topology_file="${VM_TOPOLOGY:-${repo_root}/provisioning/config/topology.yml}"
action="${1:-}"

case "${action}" in
  up|status|logs|down|config) ;;
  *)
    printf 'usage: %s {up|status|logs|down|config}\n' "$0" >&2
    exit 2
    ;;
esac

mapfile -t portal_values < <(/usr/bin/python3 - "${topology_file}" <<'PY'
import re
import sys
from pathlib import Path

import yaml

topology = yaml.safe_load(Path(sys.argv[1]).read_text())
environment_id = topology["environmentId"]
if not re.fullmatch(r"[a-z][a-z0-9-]{2,31}", environment_id):
    raise SystemExit("invalid environmentId")
control = topology["networks"]["control"]
portal = topology["portal"]
values = [
    environment_id,
    topology["domain"],
    topology["portal"]["composeFile"],
    str(topology["portal"]["teamNumber"]),
    topology["portal"]["teamHash"],
    control["hostAddress"],
    str(portal["apiHostPort"]),
    portal["uiHostAddress"],
    str(portal["uiHostPort"]),
]
if any("\n" in value for value in values):
    raise SystemExit("topology values must be single-line")
print(*values, sep="\n")
PY
)

environment_id="${VM_ENV_ID:-${portal_values[0]}}"
if [[ ! "${environment_id}" =~ ^[a-z][a-z0-9-]{2,31}$ ]]; then
  printf 'invalid VM_ENV_ID: %s\n' "${environment_id}" >&2
  exit 2
fi
domain="${portal_values[1]}"
compose_file="${repo_root}/${portal_values[2]}"
team_number="${portal_values[3]}"
team_hash="${portal_values[4]}"
api_address="${portal_values[5]}"
api_port="${portal_values[6]}"
ui_address="${portal_values[7]}"
ui_port="${portal_values[8]}"
project="isureal-${environment_id}-portal"
state_dir="${repo_root}/provisioning/.state/${environment_id}"
env_file="${state_dir}/portal.env"

write_env() {
  install -d -m 0700 "${state_dir}"
  umask 077
  {
    printf 'PORTAL_TEAM_NUMBER=%s\n' "${team_number}"
    printf 'PORTAL_TEAM_HASH=%s\n' "${team_hash}"
    printf 'PORTAL_DOMAIN_SUFFIX=%s\n' "${domain}"
    printf 'PORTAL_ROUTER_MODE=external-router\n'
    printf 'PORTAL_SERVER_BIND_IP=%s\n' "${api_address}"
    printf 'PORTAL_SERVER_PORT=%s\n' "${api_port}"
    printf 'PORTAL_WEB_BIND_IP=%s\n' "${ui_address}"
    printf 'PORTAL_WEB_PORT=%s\n' "${ui_port}"
  } >"${env_file}"
}

compose() {
  docker compose --project-name "${project}" --env-file "${env_file}" -f "${compose_file}" "$@"
}

if [[ "${action}" == "up" || "${action}" == "config" ]]; then
  write_env
elif [[ ! -f "${env_file}" ]]; then
  printf 'Portal environment state is missing: %s\n' "${env_file}" >&2
  exit 4
fi

case "${action}" in
  up)
    if ! ip -4 address show | grep -Fq "${api_address}/"; then
      printf 'Portal control address %s is not present on the host\n' "${api_address}" >&2
      exit 4
    fi
    # BuildKit's default provenance attestation carries build-specific
    # metadata, so an otherwise no-op build can produce a new manifest list
    # and make Compose restart healthy containers on every reprovision.
    compose build --provenance=false --sbom=false
    compose up -d --wait
    ;;
  status)
    # `compose images` fails when a no-op rebuild replaces the tag while an
    # otherwise healthy container still references the previous content ID.
    # `ps --no-trunc` reports the actual container image IDs, health, and
    # publishes without requiring that stale image metadata to remain tagged.
    compose ps --all --no-trunc
    ;;
  logs)
    compose logs --no-color --tail "${VM_LOG_LINES:-200}"
    ;;
  down)
    compose down --remove-orphans
    ;;
  config)
    compose config
    ;;
esac
