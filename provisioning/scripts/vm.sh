#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
topology_file="${VM_TOPOLOGY:-${repo_root}/provisioning/config/topology.yml}"
release_file="${VM_RELEASE:-${repo_root}/provisioning/config/release.json}"
action="${1:-}"

mapfile -t environment_values < <(mise exec -- python - "${topology_file}" "${release_file}" <<'PY'
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
print(topology["clientTlsForward"]["hostPort"])
print(topology["clientTlsForward"]["guestPort"])
PY
)
environment_id="${VM_ENV_ID:-${environment_values[0]}}"
release_id="${environment_values[1]}"
tls_host_port="${environment_values[2]}"
tls_guest_port="${environment_values[3]}"
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

# VirtualBox sometimes leaves an empty per-VM folder (for example a lingering
# .nvram file) after a machine is unregistered.  On the next `vagrant up`, the
# box is imported under a temporary name and then renamed to the environment VM
# name, and that rename fails because the destination folder already exists.
# Remove only this environment's own leftover folders, and only when no VM of
# that exact name is still registered, so a healthy environment is never touched.
remove_stale_vm_folders() {
  local machine_folder
  machine_folder="$(VBoxManage list systemproperties 2>/dev/null | sed -n 's/^Default machine folder: *//p')"
  if [[ -z "${machine_folder}" || "${machine_folder}" == "/" || ! -d "${machine_folder}" ]]; then
    return 0
  fi
  local node name folder
  for node in app1 app2 app3 db1 router; do
    name="${environment_id}-${node}"
    if VBoxManage showvminfo "${name}" >/dev/null 2>&1; then
      continue
    fi
    folder="${machine_folder%/}/${name}"
    if [[ -d "${folder}" ]]; then
      printf 'Removing stale VirtualBox folder: %s\n' "${folder}" >&2
      rm -rf -- "${folder}"
    fi
  done
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
  mise exec -- ansible-playbook -i "${inventory_file}" "${repo_root}/provisioning/ansible/playbooks/runtime.yml"
}

validate_runtime() {
  if [[ ! -f "${inventory_file}" ]]; then
    render_inventory
  fi
  mise exec -- ansible-playbook -i "${inventory_file}" "${repo_root}/provisioning/ansible/playbooks/validate.yml"
  mise exec -- python "${repo_root}/provisioning/tests/conformance/validate.py" \
    --topology "${topology_file}" --release "${release_file}"
}

preflight_up() {
  "${repo_root}/provisioning/scripts/preflight.sh" --skip-artifacts
  "${repo_root}/provisioning/scripts/build-artifacts.sh"
  "${repo_root}/provisioning/scripts/preflight.sh"
}

ensure_tls() {
  "${repo_root}/provisioning/scripts/tls.sh"
}

ensure_router_forwards() {
  local ports
  ports="$(vagrant_cmd port router 2>/dev/null || true)"
  if grep -Eq "^[[:space:]]*${tls_guest_port} \\(guest\\) => ${tls_host_port} \\(host\\)$" <<<"${ports}"; then
    return
  fi
  printf 'Applying Router TLS forward %s -> %s\n' "${tls_host_port}" "${tls_guest_port}"
  vagrant_cmd reload router --no-provision
}

case "${action}" in
  preflight)
    exec "${repo_root}/provisioning/scripts/preflight.sh"
    ;;
  up)
    preflight_up
    ensure_tls
    ensure_images
    install -d -m 0700 "${state_dir}"
    remove_stale_vm_folders
    vagrant_cmd up --provider=virtualbox --no-provision
    "${repo_root}/provisioning/scripts/portal-compose.sh" up
    provision_runtime
    validate_runtime
    ;;
  provision)
    "${repo_root}/provisioning/scripts/preflight.sh" --skip-ports --skip-routes
    ensure_tls
    ensure_router_forwards
    "${repo_root}/provisioning/scripts/portal-compose.sh" up
    provision_runtime
    validate_runtime
    ;;
  validate)
    validate_runtime
    ;;
  status)
    printf 'environment=%s release=%s\n' "${environment_id}" "${release_id}"
    printf 'tls-ca=%s\n' "${repo_root}/provisioning/.state/tls/local-ca.crt"
    vagrant_cmd status
    "${repo_root}/provisioning/scripts/portal-compose.sh" status
    if [[ -f "${state_dir}/reports/latest.json" ]]; then
      jq '{outcome,generatedAt,errors}' "${state_dir}/reports/latest.json"
    fi
    ;;
  logs)
    for node in app1 app2 app3 db1 router; do
      printf '\n[%s]\n' "${node}"
      vagrant_cmd ssh "${node}" -c "sudo journalctl --no-pager -n ${VM_LOG_LINES:-200} -u 'isureal-*' -u nginx -u postgresql" 2>&1 || true
    done | sed -E 's#(postgresql://[^:[:space:]]+:)[^@[:space:]]+#\1[REDACTED]#g; s/(HMAC|TOKEN|PASSWORD|PRIVATE_KEY)=([^[:space:]]+)/\1=[REDACTED]/g'
    "${repo_root}/provisioning/scripts/portal-compose.sh" logs
    ;;
  reset-data)
    render_inventory
    mise exec -- ansible-playbook -i "${inventory_file}" "${repo_root}/provisioning/ansible/playbooks/reset-data.yml"
    validate_runtime
    ;;
  destroy)
    if [[ -f "${state_dir}/portal.env" ]]; then
      "${repo_root}/provisioning/scripts/portal-compose.sh" down
    fi
    vagrant_cmd destroy --force
    remove_stale_vm_folders
    if [[ -d "${state_dir}" ]]; then
      rm -rf -- "${state_dir}"
    fi
    ;;
  *)
    printf 'usage: %s {preflight|up|provision|validate|status|logs|reset-data|destroy}\n' "$0" >&2
    exit 2
    ;;
esac
