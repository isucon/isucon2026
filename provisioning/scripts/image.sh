#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_file="${VM_RELEASE:-${repo_root}/provisioning/config/release.json}"
base_root="${repo_root}/provisioning/packer/local-base"
role_root="${repo_root}/provisioning/packer/role-images"
index_file="${repo_root}/provisioning/.state/images/image-artifact-index.json"
action="${1:-}"
architecture="${3:-${2:-amd64}}"

read_release() {
  mise exec -- python - "${release_file}" "$1" <<'PY'
import json
import sys
value = json.load(open(sys.argv[1]))
for part in sys.argv[2].split('.'):
    value = value[part]
print(value)
PY
}

release_id="$(read_release releaseId)"
iso_url="$(read_release ubuntu.installImages.amd64.url)"
iso_sha="$(read_release ubuntu.installImages.amd64.sha256)"
source_revision="$(read_release sourceRevision)"
image_dir="${repo_root}/provisioning/.state/images/${release_id}"
base_box="${image_dir}/ubuntu-24.04.4-amd64-virtualbox.box"

require_architecture() {
  if [[ "${architecture}" != "amd64" ]]; then
    printf 'Only amd64 image builds are supported; got %s\n' "${architecture}" >&2
    exit 2
  fi
}

register_box() {
  local name="$1"
  local path="$2"
  vagrant box add --name "${name}" --provider virtualbox --force "${path}"
}

validate_all() {
  "${repo_root}/provisioning/tests/static/run.sh"
  "${repo_root}/provisioning/scripts/preflight.sh" --skip-ports --skip-routes
  mise exec -- packer init "${base_root}"
  mise exec -- packer validate \
    -var "architecture=amd64" \
    -var "release_id=${release_id}" \
    -var "ubuntu_iso_url=${iso_url}" \
    -var "ubuntu_iso_checksum=sha256:${iso_sha}" \
    "${base_root}"
  mise exec -- packer init "${role_root}"
  mise exec -- packer inspect "${role_root}" >/dev/null
  ANSIBLE_CONFIG="${repo_root}/provisioning/ansible/ansible.cfg" \
    mise exec -- ansible-playbook -i 'localhost,' --connection=local --syntax-check \
    "${repo_root}/provisioning/ansible/playbooks/image.yml"
  if [[ -f "${base_box}" ]]; then
    mise exec -- packer validate \
      -var "architecture=amd64" \
      -var "release_id=${release_id}" \
      -var "base_box_path=${base_box}" \
      -var "release_manifest_path=${release_file}" \
      -var "artifact_root=${repo_root}/provisioning/.state/artifacts/current/bundles" \
      "${role_root}"
  fi
  if [[ -f "${index_file}" ]]; then
    mise exec -- python "${repo_root}/provisioning/scripts/manifest.py" --index "${index_file}" validate
  fi
}

build_base() {
  require_architecture
  mkdir -p "${image_dir}"
  if [[ -e "${base_box}" ]]; then
    printf 'Immutable base box already exists: %s\n' "${base_box}" >&2
    exit 3
  fi
  PACKER_LOG_PATH="${image_dir}/base-packer.log" \
    mise exec -- packer build \
      -var "architecture=amd64" \
      -var "release_id=${release_id}" \
      -var "ubuntu_iso_url=${iso_url}" \
      -var "ubuntu_iso_checksum=sha256:${iso_sha}" \
      "${base_root}"
  register_box "isureal/base-${release_id}" "${base_box}"
  mise exec -- python "${repo_root}/provisioning/scripts/manifest.py" --index "${index_file}" record-box \
    --release "${release_file}" --role base --box "${base_box}" \
    --source-image-id "ubuntu-24.04.4-amd64@sha256:${iso_sha}" \
    --build-name ubuntu-noble-amd64-base
}

build_role() {
  local role="${2:-}"
  require_architecture
  case "${role}" in
    problem-server|router) ;;
    *) printf 'ROLE must be problem-server or router\n' >&2; exit 2 ;;
  esac
  if [[ ! -f "${base_box}" ]]; then
    printf 'Base box is missing; run make image-build-base first: %s\n' "${base_box}" >&2
    exit 3
  fi
  local output_dir="${image_dir}/${role}-output"
  local output_box="${image_dir}/${role}-amd64-virtualbox.box"
  if [[ -e "${output_box}" ]]; then
    printf 'Immutable role box already exists: %s\n' "${output_box}" >&2
    exit 3
  fi
  if [[ -d "${output_dir}" ]]; then
    rm -rf -- "${output_dir}"
  fi
  local only
  if [[ "${role}" == "problem-server" ]]; then
    only="problem-server-amd64.vagrant.problem_server"
  else
    only="router-amd64.vagrant.router"
  fi
  PACKER_LOG_PATH="${image_dir}/${role}-packer.log" \
    mise exec -- packer build -only="${only}" \
      -var "architecture=amd64" \
      -var "release_id=${release_id}" \
      -var "base_box_path=${base_box}" \
      -var "release_manifest_path=${release_file}" \
      -var "artifact_root=${repo_root}/provisioning/.state/artifacts/current/bundles" \
      "${role_root}"
  mv "${output_dir}/package.box" "${output_box}"
  rmdir "${output_dir}" 2>/dev/null || true
  register_box "isureal/${role}-${release_id}" "${output_box}"
  mise exec -- python "${repo_root}/provisioning/scripts/manifest.py" --index "${index_file}" record-box \
    --release "${release_file}" --role "${role}" --box "${output_box}" \
    --source-image-id "${release_id}/base/virtualbox/amd64" \
    --build-name "${role}-amd64"
}

promote() {
  local role="${2:-}"
  require_architecture
  local artifact_id="${4:-${release_id}/${role}/virtualbox/amd64}"
  local environment_id="${VM_ENV_ID:-isureal-local}"
  if [[ ! "${environment_id}" =~ ^[a-z][a-z0-9-]{2,31}$ ]]; then
    printf 'invalid VM_ENV_ID: %s\n' "${environment_id}" >&2
    exit 2
  fi
  local report="${repo_root}/provisioning/.state/${environment_id}/reports/latest.json"
  if [[ ! -f "${report}" ]] \
    || [[ "$(jq -r .outcome "${report}")" != "pass" ]] \
    || [[ "$(jq -r .environmentId "${report}")" != "${environment_id}" ]] \
    || [[ "$(jq -r .releaseId "${report}")" != "${release_id}" ]]; then
    printf 'A passing Linux amd64 conformance report is required: %s\n' "${report}" >&2
    exit 3
  fi
  local report_uri="file://provisioning/.state/${environment_id}/reports/latest.json"
  mise exec -- python "${repo_root}/provisioning/scripts/manifest.py" --index "${index_file}" set-state \
    --artifact-id "${artifact_id}" --state validated --report "${report_uri}"
  mise exec -- python "${repo_root}/provisioning/scripts/manifest.py" --index "${index_file}" set-state \
    --artifact-id "${artifact_id}" --state promoted --report "linux-amd64-virtualbox:${source_revision}"
}

case "${action}" in
  validate) validate_all ;;
  build-base) build_base ;;
  build-role) build_role "$@" ;;
  promote) promote "$@" ;;
  *) printf 'usage: %s {validate|build-base amd64|build-role ROLE amd64|promote ROLE amd64 [ARTIFACT_ID]}\n' "$0" >&2; exit 2 ;;
esac
