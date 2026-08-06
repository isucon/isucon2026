#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
topology_file="${VM_TOPOLOGY:-${repo_root}/provisioning/config/topology.yml}"
release_file="${VM_RELEASE:-${repo_root}/provisioning/config/release.json}"
action="${1:-}"

mapfile -t environment_values < <(/usr/bin/python3 - "${topology_file}" "${release_file}" <<'PY'
import json
import re
import sys
from pathlib import Path
import yaml

topology = yaml.safe_load(Path(sys.argv[1]).read_text())
release = json.loads(Path(sys.argv[2]).read_text())
environment_id = topology["environmentId"]
if not re.fullmatch(r"[a-z][a-z0-9-]{2,31}", environment_id):
    raise SystemExit("invalid environmentId")
print(environment_id)
print(release["releaseId"])
PY
)
environment_id="${VM_ENV_ID:-${environment_values[0]}}"
release_id="${environment_values[1]}"
if [[ ! "${environment_id}" =~ ^[a-z][a-z0-9-]{2,31}$ ]]; then
  printf 'invalid VM_ENV_ID: %s\n' "${environment_id}" >&2
  exit 2
fi
state_dir="${repo_root}/provisioning/.state/${environment_id}"
inventory_file="${state_dir}/inventory.yml"
image_dir="${repo_root}/provisioning/.state/images/${release_id}"
base_box="${image_dir}/ubuntu-24.04.4-amd64-virtualbox.box"
problem_box="${image_dir}/problem-server-amd64-virtualbox.box"
router_box="${image_dir}/router-amd64-virtualbox.box"
vagrant_dir="${repo_root}/provisioning/vagrant"
export VM_ENV_ID="${environment_id}"
export VM_TOPOLOGY="${topology_file}"
export VM_RELEASE="${release_file}"
export VAGRANT_DOTFILE_PATH="${state_dir}/vagrant"
export ANSIBLE_CONFIG="${repo_root}/provisioning/ansible/ansible.cfg"

vagrant_cmd() {
  (cd "${vagrant_dir}" && vagrant "$@")
}

box_registered() {
  local name="$1"
  vagrant box list --machine-readable | awk -F, -v wanted="${name}" '$3 == "box-name" && $4 == wanted {found=1} END {exit !found}'
}

ensure_registered_box() {
  local name="$1"
  local path="$2"
  if ! box_registered "${name}"; then
    vagrant box add --name "${name}" --provider virtualbox --force "${path}"
  fi
}

ensure_images() {
  if [[ ! -f "${base_box}" ]]; then
    "${repo_root}/provisioning/scripts/image.sh" build-base amd64
  fi
  ensure_registered_box "isureal/base-${release_id}" "${base_box}"
  if [[ ! -f "${problem_box}" ]]; then
    "${repo_root}/provisioning/scripts/image.sh" build-role problem-server amd64
  fi
  if [[ ! -f "${router_box}" ]]; then
    "${repo_root}/provisioning/scripts/image.sh" build-role router amd64
  fi
  ensure_registered_box "isureal/problem-server-${release_id}" "${problem_box}"
  ensure_registered_box "isureal/router-${release_id}" "${router_box}"
}

render_inventory() {
  install -d -m 0700 "${state_dir}"
  "${repo_root}/provisioning/scripts/inventory.sh" --topology "${topology_file}" --release "${release_file}" --output "${inventory_file}" >/dev/null
}

provision_runtime() {
  render_inventory
  ansible-playbook -i "${inventory_file}" "${repo_root}/provisioning/ansible/playbooks/runtime.yml"
}

validate_runtime() {
  if [[ ! -f "${inventory_file}" ]]; then
    render_inventory
  fi
  ansible-playbook -i "${inventory_file}" "${repo_root}/provisioning/ansible/playbooks/validate.yml"
  /usr/bin/python3 "${repo_root}/provisioning/tests/conformance/validate.py" --topology "${topology_file}"
}

preflight_up() {
  "${repo_root}/provisioning/scripts/preflight.sh" --skip-artifacts
  "${repo_root}/provisioning/scripts/build-artifacts.sh"
  "${repo_root}/provisioning/scripts/preflight.sh"
}

case "${action}" in
  preflight)
    exec "${repo_root}/provisioning/scripts/preflight.sh"
    ;;
  up)
    preflight_up
    ensure_images
    install -d -m 0700 "${state_dir}"
    vagrant_cmd up --provider=virtualbox --no-provision
    "${repo_root}/provisioning/scripts/portal-compose.sh" up
    provision_runtime
    validate_runtime
    ;;
  provision)
    "${repo_root}/provisioning/scripts/preflight.sh" --skip-ports --skip-routes
    "${repo_root}/provisioning/scripts/portal-compose.sh" up
    provision_runtime
    validate_runtime
    ;;
  validate)
    validate_runtime
    ;;
  status)
    printf 'environment=%s release=%s\n' "${environment_id}" "${release_id}"
    vagrant_cmd status
    "${repo_root}/provisioning/scripts/portal-compose.sh" status
    if [[ -f "${state_dir}/reports/latest.json" ]]; then
      jq '{outcome,generatedAt,errors}' "${state_dir}/reports/latest.json"
    fi
    ;;
  logs)
    for node in server1 server2 server3 router; do
      printf '\n[%s]\n' "${node}"
      vagrant_cmd ssh "${node}" -c "sudo journalctl --no-pager -n ${VM_LOG_LINES:-200} -u 'isureal-*' -u nginx -u postgresql" 2>&1 || true
    done | sed -E 's#(postgresql://[^:[:space:]]+:)[^@[:space:]]+#\1[REDACTED]#g; s/(HMAC|TOKEN|PASSWORD|PRIVATE_KEY)=([^[:space:]]+)/\1=[REDACTED]/g'
    "${repo_root}/provisioning/scripts/portal-compose.sh" logs
    ;;
  reset-data)
    if [[ "${CONFIRM_RESET:-}" != "${environment_id}" ]]; then
      printf 'Set CONFIRM_RESET=%s to reset PostgreSQL data on server1..3\n' "${environment_id}" >&2
      exit 7
    fi
    render_inventory
    ansible-playbook -i "${inventory_file}" "${repo_root}/provisioning/ansible/playbooks/reset-data.yml"
    validate_runtime
    ;;
  destroy)
    if [[ "${CONFIRM_DESTROY:-}" != "${environment_id}" ]]; then
      printf 'Set CONFIRM_DESTROY=%s to destroy only this environment\n' "${environment_id}" >&2
      exit 7
    fi
    if [[ -f "${state_dir}/portal.env" ]]; then
      "${repo_root}/provisioning/scripts/portal-compose.sh" down
    fi
    vagrant_cmd destroy --force
    if [[ -d "${state_dir}" ]]; then
      rm -rf -- "${state_dir}"
    fi
    ;;
  *)
    printf 'usage: %s {preflight|up|provision|validate|status|logs|reset-data|destroy}\n' "$0" >&2
    exit 2
    ;;
esac
