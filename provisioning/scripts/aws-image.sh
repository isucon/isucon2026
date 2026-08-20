#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_file="${VM_RELEASE:-${repo_root}/provisioning/config/release.json}"
packer_root="${repo_root}/provisioning/packer/aws-images"
index_file="${repo_root}/provisioning/.state/images/image-artifact-index.json"
action="${1:-}"
role="${2:-}"
region="${AWS_REGION:-${AWS_DEFAULT_REGION:-}}"

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
default_region="$(read_release ubuntu.cloudImages.amd64.region)"
source_ami="$(read_release ubuntu.cloudImages.amd64.amiId)"
source_owner="$(read_release ubuntu.cloudImages.amd64.ownerId)"
region="${region:-${default_region}}"
artifact_root="${repo_root}/provisioning/.state/artifacts/current/bundles"
image_dir="${repo_root}/provisioning/.state/images/${release_id}"

require_role() {
  case "${role}" in
    problem-server|router) ;;
    *) printf 'ROLE must be problem-server or router\n' >&2; exit 2 ;;
  esac
}

verify_source_ami() {
  local actual
  actual="$(
    aws ec2 describe-images \
      --region "${region}" \
      --image-ids "${source_ami}" \
      --query 'Images[0].[OwnerId,Architecture,State]' \
      --output text
  )"
  if [[ "${actual}" != "${source_owner}"$'\t'"x86_64"$'\t'"available" ]]; then
    printf 'Pinned source AMI is not the expected available Canonical amd64 image: %s (%s)\n' \
      "${source_ami}" "${actual}" >&2
    exit 3
  fi
}

configure_aws_sdk_credentials() {
  local original_config="${AWS_CONFIG_FILE:-${HOME}/.aws/config}"
  local credential_dir="${image_dir}/.aws-sdk"
  mkdir -p "${credential_dir}"
  chmod 0700 "${credential_dir}"
  local helper="${credential_dir}/export-credentials.sh"
  local sdk_config="${credential_dir}/config"
  cat >"${helper}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
AWS_CONFIG_FILE='${original_config}' AWS_PROFILE='${AWS_PROFILE:-default}' \
  exec aws configure export-credentials --format process
EOF
  chmod 0700 "${helper}"
  cat >"${sdk_config}" <<EOF
[default]
region = ${region}
credential_process = ${helper}
EOF
  chmod 0600 "${sdk_config}"
  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_CREDENTIAL_EXPIRATION
  export AWS_CONFIG_FILE="${sdk_config}"
  export AWS_PROFILE=default
  export AWS_SDK_LOAD_CONFIG=1
}

record_ami() {
  local ami_id="$1"
  local build_time="$2"
  local owner_id
  owner_id="$(aws sts get-caller-identity --query Account --output text)"
  mise exec -- python "${repo_root}/provisioning/scripts/manifest.py" \
    --index "${index_file}" record-ami \
    --release "${release_file}" \
    --role "${role}" \
    --region "${region}" \
    --ami-id "${ami_id}" \
    --owner-id "${owner_id}" \
    --source-image-id "${source_ami}" \
    --build-name "${role}-amd64" \
    --build-time "${build_time}"
}

adopt_existing_ami() {
  local expected_name="isucon2026-${release_id}-${role}-amd64"
  local images count ami_id build_time
  images="$(
    aws ec2 describe-images \
      --region "${region}" \
      --owners self \
      --filters \
        "Name=name,Values=${expected_name}" \
        "Name=state,Values=available" \
        "Name=tag:ReleaseId,Values=${release_id}" \
        "Name=tag:Role,Values=${role}" \
      --query 'Images[].{amiId:ImageId,buildTime:CreationDate}' \
      --output json
  )"
  count="$(jq 'length' <<<"${images}")"
  if [[ "${count}" == "0" ]]; then
    return 1
  fi
  if [[ "${count}" != "1" ]]; then
    printf 'Expected exactly one recoverable AMI named %s, found %s\n' \
      "${expected_name}" "${count}" >&2
    exit 3
  fi
  ami_id="$(jq -er '.[0].amiId' <<<"${images}")"
  build_time="$(jq -er '.[0].buildTime' <<<"${images}")"
  record_ami "${ami_id}" "${build_time}"
  printf 'Recovered existing AMI: %s\n' "${ami_id}" >&2
  printf '%s\n' "${ami_id}"
}

packer_args() {
  local manifest="$1"
  printf '%s\n' \
    -var "architecture=amd64" \
    -var "aws_region=${region}" \
    -var "source_ami_id=${source_ami}" \
    -var "source_ami_owner_id=${source_owner}" \
    -var "release_id=${release_id}" \
    -var "release_manifest_path=${release_file}" \
    -var "artifact_root=${artifact_root}" \
    -var "build_instance_type=${AMI_BUILD_INSTANCE_TYPE:-t3.medium}" \
    -var "subnet_id=${AMI_BUILD_SUBNET_ID:-}" \
    -var "vpc_id=${AMI_BUILD_VPC_ID:-}" \
    -var "manifest_path=${manifest}"
}

validate_all() {
  verify_source_ami
  mkdir -p "${image_dir}"
  local manifest="${image_dir}/aws-validation-manifest.json"
  mapfile -t args < <(packer_args "${manifest}")
  mise exec -- packer init "${packer_root}"
  mise exec -- packer validate "${args[@]}" "${packer_root}"
}

build_role() {
  require_role
  verify_source_ami
  if [[ ! -d "${artifact_root}" ]]; then
    printf 'Application artifacts are missing; run make provisioning-artifacts first: %s\n' "${artifact_root}" >&2
    exit 3
  fi
  mkdir -p "${image_dir}"
  local manifest="${image_dir}/${role}-amazon-ebs-manifest.json"
  local existing_id="${release_id}/${role}/amazon-ebs/amd64/${region}"
  if [[ -f "${index_file}" ]] \
    && jq -e --arg id "${existing_id}" '.artifacts[] | select(.artifactId == $id)' "${index_file}" >/dev/null; then
    printf 'Immutable AMI artifact already exists; use a new release ID: %s\n' "${existing_id}" >&2
    exit 3
  fi
  if adopt_existing_ami; then
    return
  fi
  if [[ -e "${manifest}" ]]; then
    printf 'Packer manifest already exists without an indexed immutable artifact; inspect it before retrying: %s\n' "${manifest}" >&2
    exit 3
  fi
  configure_aws_sdk_credentials
  mapfile -t args < <(packer_args "${manifest}")
  local only
  if [[ "${role}" == "problem-server" ]]; then
    only="problem-server-amd64.amazon-ebs.problem_server"
  else
    only="router-amd64.amazon-ebs.router"
  fi
  PACKER_LOG_PATH="${image_dir}/${role}-amazon-ebs-packer.log" \
    mise exec -- packer build -only="${only}" "${args[@]}" "${packer_root}"

  local artifact_id ami_id build_time
  artifact_id="$(jq -er '.builds[-1].artifact_id' "${manifest}")"
  ami_id="${artifact_id#*:}"
  if [[ ! "${ami_id}" =~ ^ami-[0-9a-f]+$ ]]; then
    printf 'Unexpected Packer AMI artifact ID: %s\n' "${artifact_id}" >&2
    exit 3
  fi
  build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  record_ami "${ami_id}" "${build_time}"
  printf '%s\n' "${ami_id}"
}

case "${action}" in
  validate) validate_all ;;
  build-role) build_role ;;
  *)
    printf 'usage: %s {validate|build-role ROLE}\n' "$0" >&2
    exit 2
    ;;
esac
