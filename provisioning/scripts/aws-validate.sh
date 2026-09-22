#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
aws_root="${repo_root}/provisioning/aws"
stack="${STACK:-dev}"
region="${AWS_REGION:-${AWS_DEFAULT_REGION:-ap-northeast-1}}"
report_dir="${repo_root}/provisioning/.state/aws/${stack}/reports"
index_file="${repo_root}/provisioning/.state/images/image-artifact-index.json"
manifest_script="${repo_root}/provisioning/scripts/manifest.py"

command_id() {
  aws ssm send-command \
    --region "${region}" \
    --instance-ids "$1" \
    --document-name AWS-RunShellScript \
    --parameters "$(jq -cn --arg command "$2" '{commands:[$command]}')" \
    --query 'Command.CommandId' \
    --output text
}

run_ssm() {
  local instance_id="$1"
  local command="$2"
  local id status
  id="$(command_id "${instance_id}" "${command}")"
  for _ in $(seq 1 60); do
    status="$(
      aws ssm get-command-invocation \
        --region "${region}" \
        --command-id "${id}" \
        --instance-id "${instance_id}" \
        --query Status \
        --output text 2>/dev/null || true
    )"
    case "${status}" in
      Success)
        aws ssm get-command-invocation \
          --region "${region}" \
          --command-id "${id}" \
          --instance-id "${instance_id}" \
          --query StandardOutputContent \
          --output text
        return 0
        ;;
      Failed|Cancelled|TimedOut)
        aws ssm get-command-invocation \
          --region "${region}" \
          --command-id "${id}" \
          --instance-id "${instance_id}" \
          --query StandardErrorContent \
          --output text >&2 || true
        return 3
        ;;
    esac
    sleep 5
  done
  printf 'SSM command timed out: %s %s\n' "${instance_id}" "${id}" >&2
  return 3
}

run_ssm_retry() {
  local instance_id="$1"
  local command="$2"
  local attempts="${3:-30}"
  for _ in $(seq 1 "${attempts}"); do
    if run_ssm "${instance_id}" "${command}"; then
      return 0
    fi
    sleep 5
  done
  printf 'SSM command did not converge on %s: %s\n' "${instance_id}" "${command}" >&2
  return 3
}

peer_connect_all() {
  for source_index in 0 1 2 3; do
    local targets=()
    for target_index in 0 1 2 3; do
      if [[ "${source_index}" -ne "${target_index}" ]]; then
        local port=8080
        [[ "${target_index}" -ne 3 ]] || port=5432
        targets+=("timeout 3 bash -c 'exec 3<>/dev/tcp/${service_ips[$target_index]}/${port}' || exit 1")
      fi
    done
    run_ssm "${service_ids[$source_index]}" "$(IFS='; '; echo "${targets[*]}")"
  done
}

global_ip_isolation_all() {
  local node_ids=("${router_id}" "${service_ids[@]}")
  local public_ips=("${router_public_ip}" "${service_public_ips[@]}")
  local -a checks
  local source_index target_index source_id source_public target_public
  local subnet_id acl_json egress
  for source_index in 0 1 2 3 4; do
    source_id="${node_ids[$source_index]}"
    source_public="${public_ips[$source_index]}"
    checks=(
      "observed_public=\$(curl -4fsS --connect-timeout 5 https://checkip.amazonaws.com | tr -d '[:space:]')"
      "[ \"\$observed_public\" = '${source_public}' ] || { echo 'unexpected public egress identity' >&2; exit 1; }"
    )
    for target_index in 0 1 2 3 4; do
      if [[ "${source_index}" -eq "${target_index}" ]]; then
        continue
      fi
      target_public="${public_ips[$target_index]}"
      checks+=(
        "if ping -c 1 -W 1 '${target_public}' >/dev/null 2>&1; then echo 'Global IP ICMP bypassed isolation: ${source_public} -> ${target_public}' >&2; exit 1; fi"
        "if timeout 2 bash -c 'exec 3<>/dev/tcp/${target_public}/22' >/dev/null 2>&1; then echo 'Global IP TCP bypassed isolation: ${source_public} -> ${target_public}' >&2; exit 1; fi"
      )
    done
    run_ssm "${source_id}" "$(IFS='; '; echo "${checks[*]}")"

    subnet_id="$(
      aws ec2 describe-instances \
        --region "${region}" \
        --instance-ids "${source_id}" \
        --query 'Reservations[0].Instances[0].NetworkInterfaces[?Attachment.DeviceIndex==`0`].SubnetId | [0]' \
        --output text
    )"
    acl_json="$(
      aws ec2 describe-network-acls \
        --region "${region}" \
        --filters "Name=association.subnet-id,Values=${subnet_id}" \
        --output json
    )"
    if [[ "$(jq '.NetworkAcls | length' <<<"${acl_json}")" -ne 1 ]]; then
      printf 'Expected one Network ACL for %s subnet %s\n' "${source_id}" "${subnet_id}" >&2
      return 3
    fi
    for egress in true false; do
      jq -e \
        --argjson egress "${egress}" '
          any(.NetworkAcls[0].Entries[];
            .Egress == $egress
            and .RuleNumber == 1000
            and .RuleAction == "allow"
            and .Protocol == "-1"
            and .CidrBlock == "0.0.0.0/0")
        ' <<<"${acl_json}" >/dev/null
    done
    for target_index in 0 1 2 3 4; do
      if [[ "${source_index}" -eq "${target_index}" ]]; then
        continue
      fi
      target_public="${public_ips[$target_index]}"
      for egress in true false; do
        if ! jq -e \
          --arg cidr "${target_public}/32" \
          --argjson egress "${egress}" '
            any(.NetworkAcls[0].Entries[];
              .Egress == $egress
              and .RuleNumber < 1000
              and .RuleAction == "deny"
              and .Protocol == "-1"
              and .CidrBlock == $cidr)
          ' <<<"${acl_json}" >/dev/null; then
          printf 'Network ACL on %s does not deny %s (egress=%s)\n' \
            "${source_id}" "${target_public}" "${egress}" >&2
          return 3
        fi
      done
    done
  done
}

nft_packet_count() {
  local snapshot="$1"
  local direction="$2"
  local line
  line="$(grep -F "comment \"${direction}\"" <<<"${snapshot}" || true)"
  if [[ "${line}" =~ counter[[:space:]]+packets[[:space:]]+([0-9]+) ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  printf 'Router counter is missing for %s: %s\n' "${direction}" "${line}" >&2
  return 3
}

tc_packet_count() {
  local snapshot="$1"
  local device="$2"
  jq -er --arg device "${device}" '
    [.[] | select(.dev == $device and .kind == "htb" and .root == true) | .packets]
    | if length == 1 then .[0] else error("missing root HTB qdisc for " + $device) end
  ' <<<"${snapshot}"
}

record_validation() {
  local artifact_id="$1"
  local report_uri="$2"
  local state
  state="$(
    jq -er --arg artifact_id "${artifact_id}" \
      '.artifacts[] | select(.artifactId == $artifact_id) | .validationState' \
      "${index_file}"
  )"
  case "${state}" in
    unverified|validated)
      mise exec -- python "${manifest_script}" --index "${index_file}" set-state \
        --artifact-id "${artifact_id}" --state validated --report "${report_uri}" >/dev/null
      ;;
    promoted)
      ;;
    *)
      printf 'AMI artifact cannot accept a passing validation report in state %s: %s\n' \
        "${state}" "${artifact_id}" >&2
      return 3
      ;;
  esac
}

mapfile -t service_ids < <(
  pulumi -C "${aws_root}" stack output serviceInstanceIds --stack "${stack}" --json |
    jq -er '.[]'
)
mapfile -t service_ips < <(
  pulumi -C "${aws_root}" stack output servicePrivateIps --stack "${stack}" --json |
    jq -er '.[]'
)
mapfile -t service_public_ips < <(
  pulumi -C "${aws_root}" stack output servicePublicAddresses --stack "${stack}" --json |
    jq -er '.[]'
)
router_id="$(pulumi -C "${aws_root}" stack output routerInstanceId --stack "${stack}")"
router_public_ip="$(pulumi -C "${aws_root}" stack output routerPublicAddress --stack "${stack}")"
app_hostname="$(pulumi -C "${aws_root}" stack output appHostname --stack "${stack}")"
release_id="$(pulumi -C "${aws_root}" config get releaseId --stack "${stack}")"
service_ami_id="$(pulumi -C "${aws_root}" config get serviceAmiId --stack "${stack}")"
router_ami_id="$(pulumi -C "${aws_root}" config get routerAmiId --stack "${stack}")"
service_artifact_id="${release_id}/problem-server/amazon-ebs/amd64/${region}"
router_artifact_id="${release_id}/router/amazon-ebs/amd64/${region}"
router_stopped=0

restore_router() {
  if [[ "${router_stopped}" == "1" ]]; then
    aws ec2 start-instances --region "${region}" --instance-ids "${router_id}" >/dev/null || true
  fi
}
trap restore_router EXIT

if [[ "${#service_ids[@]}" -ne 4 || "${#service_ips[@]}" -ne 4 \
  || "${#service_public_ips[@]}" -ne 4 ]]; then
  printf 'Pulumi stack must expose exactly four service nodes and Global IPs\n' >&2
  exit 3
fi

for instance_id in "${service_ids[@]}"; do
  actual_ami_id="$(
    aws ec2 describe-instances \
      --region "${region}" \
      --instance-ids "${instance_id}" \
      --query 'Reservations[0].Instances[0].ImageId' \
      --output text
  )"
  if [[ "${actual_ami_id}" != "${service_ami_id}" ]]; then
    printf 'Service instance %s uses %s, expected %s\n' \
      "${instance_id}" "${actual_ami_id}" "${service_ami_id}" >&2
    exit 3
  fi
done
actual_router_ami_id="$(
  aws ec2 describe-instances \
    --region "${region}" \
    --instance-ids "${router_id}" \
    --query 'Reservations[0].Instances[0].ImageId' \
    --output text
)"
if [[ "${actual_router_ami_id}" != "${router_ami_id}" ]]; then
  printf 'Router instance %s uses %s, expected %s\n' \
    "${router_id}" "${actual_router_ami_id}" "${router_ami_id}" >&2
  exit 3
fi
for artifact_spec in \
  "${service_artifact_id}:${service_ami_id}" \
  "${router_artifact_id}:${router_ami_id}"; do
  artifact_id="${artifact_spec%:*}"
  expected_ami_id="${artifact_spec##*:}"
  recorded_ami_id="$(
    jq -er --arg artifact_id "${artifact_id}" \
      '.artifacts[] | select(.artifactId == $artifact_id) | .output.amiId' \
      "${index_file}"
  )"
  if [[ "${recorded_ami_id}" != "${expected_ami_id}" ]]; then
    printf 'AMI artifact %s records %s, expected %s\n' \
      "${artifact_id}" "${recorded_ami_id}" "${expected_ami_id}" >&2
    exit 3
  fi
done

for instance_id in "${service_ids[@]}"; do
  run_ssm_retry "${instance_id}" "cloud-init status --wait && sudo /usr/local/sbin/isureal-node-check"
done
run_ssm_retry "${router_id}" "cloud-init status --wait && sudo /usr/local/sbin/isureal-node-check"
run_ssm "${router_id}" "curl -fsS --connect-timeout 3 -H 'Host: ${app_hostname}' http://127.0.0.1:8080/ >/dev/null"
global_ip_isolation_all

machine_ids=()
ssh_host_keys=()
for instance_id in "${service_ids[@]}" "${router_id}"; do
  identity="$(
    run_ssm "${instance_id}" \
      "machine_id=\$(cat /etc/machine-id); host_key=\$(ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256 | awk '{print \$2}'); printf '%s %s\\n' \"\$machine_id\" \"\$host_key\""
  )"
  read -r machine_id ssh_host_key <<<"${identity}"
  if [[ ! "${machine_id}" =~ ^[0-9a-f]{32}$ || ! "${ssh_host_key}" =~ ^SHA256: ]]; then
    printf 'Invalid clone identity on %s: %s\n' "${instance_id}" "${identity}" >&2
    exit 3
  fi
  machine_ids+=("${machine_id}")
  ssh_host_keys+=("${ssh_host_key}")
done
if [[ "$(printf '%s\n' "${machine_ids[@]}" | sort -u | wc -l)" -ne 5 ]] \
  || [[ "$(printf '%s\n' "${ssh_host_keys[@]}" | sort -u | wc -l)" -ne 5 ]]; then
  printf 'Cloned instances did not generate unique machine IDs and SSH host keys\n' >&2
  exit 3
fi

before="$(run_ssm "${router_id}" "sudo nft -a list chain inet isureal_router forward")"
tc_before="$(run_ssm "${router_id}" "sudo tc -s -j qdisc show")"
peer_connect_all
after="$(run_ssm "${router_id}" "sudo nft -a list chain inet isureal_router forward")"
tc_after="$(run_ssm "${router_id}" "sudo tc -s -j qdisc show")"
nft_counter_deltas='{}'
for source in app1 app2 app3 db1; do
  for destination in app1 app2 app3 db1; do
    if [[ "${source}" == "${destination}" ]]; then
      continue
    fi
    direction="${source}-to-${destination}"
    before_packets="$(nft_packet_count "${before}" "${direction}")"
    after_packets="$(nft_packet_count "${after}" "${direction}")"
    if [[ "${after_packets}" -le "${before_packets}" ]]; then
      printf 'Router counter did not increase for %s: %s -> %s\n' \
        "${direction}" "${before_packets}" "${after_packets}" >&2
      exit 3
    fi
    nft_counter_deltas="$(
      jq -c \
        --arg direction "${direction}" \
        --argjson before "${before_packets}" \
        --argjson after "${after_packets}" \
        '.[$direction] = {before: $before, after: $after, delta: ($after - $before)}' \
        <<<"${nft_counter_deltas}"
    )"
  done
done
tc_counter_deltas='{}'
for device in app1 app2 app3 db1; do
  before_packets="$(tc_packet_count "${tc_before}" "${device}")"
  after_packets="$(tc_packet_count "${tc_after}" "${device}")"
  if [[ "${after_packets}" -le "${before_packets}" ]]; then
    printf 'Router traffic-control counter did not increase for %s: %s -> %s\n' \
      "${device}" "${before_packets}" "${after_packets}" >&2
    exit 3
  fi
  tc_counter_deltas="$(
    jq -c \
      --arg device "${device}" \
      --argjson before "${before_packets}" \
      --argjson after "${after_packets}" \
      '.[$device] = {before: $before, after: $after, delta: ($after - $before)}' \
      <<<"${tc_counter_deltas}"
  )"
done

aws ec2 stop-instances --region "${region}" --instance-ids "${router_id}" >/dev/null
router_stopped=1
aws ec2 wait instance-stopped --region "${region}" --instance-ids "${router_id}"
for source_index in 0 1 2 3; do
  checks=()
  for target_index in 0 1 2 3; do
    if [[ "${source_index}" -ne "${target_index}" ]]; then
      port=8080
      [[ "${target_index}" -ne 3 ]] || port=5432
      checks+=("if timeout 2 bash -c 'exec 3<>/dev/tcp/${service_ips[$target_index]}/${port}'; then echo 'peer path bypassed stopped Router' >&2; exit 1; fi")
    fi
  done
  run_ssm "${service_ids[$source_index]}" "$(IFS='; '; echo "${checks[*]}")"
done
aws ec2 start-instances --region "${region}" --instance-ids "${router_id}" >/dev/null
router_stopped=0
aws ec2 wait instance-running --region "${region}" --instance-ids "${router_id}"
aws ec2 wait instance-status-ok --region "${region}" --instance-ids "${router_id}"
run_ssm_retry "${router_id}" "sudo systemctl restart isureal-aws-initialize.service && sudo /usr/local/sbin/isureal-node-check"
peer_connect_all

mkdir -p "${report_dir}"
validated_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
report_name="$(date -u +%Y%m%dT%H%M%SZ).json"
report="${report_dir}/${report_name}"
report_uri="file://provisioning/.state/aws/${stack}/reports/${report_name}"
jq -n \
  --arg stack "${stack}" \
  --arg region "${region}" \
  --arg validatedAt "${validated_at}" \
  --arg releaseId "${release_id}" \
  --arg serviceAmiId "${service_ami_id}" \
  --arg routerAmiId "${router_ami_id}" \
  --arg serviceArtifactId "${service_artifact_id}" \
  --arg routerArtifactId "${router_artifact_id}" \
  --argjson serviceInstanceIds "$(printf '%s\n' "${service_ids[@]}" | jq -R . | jq -s .)" \
  --argjson servicePublicAddresses "$(printf '%s\n' "${service_public_ips[@]}" | jq -R . | jq -s .)" \
  --arg routerInstanceId "${router_id}" \
  --arg routerPublicAddress "${router_public_ip}" \
  --argjson nftCounterDeltas "${nft_counter_deltas}" \
  --argjson trafficControlCounterDeltas "${tc_counter_deltas}" \
  '{
    outcome: "pass",
    stack: $stack,
    region: $region,
    validatedAt: $validatedAt,
    releaseId: $releaseId,
    artifacts: {
      problemServer: {artifactId: $serviceArtifactId, amiId: $serviceAmiId},
      router: {artifactId: $routerArtifactId, amiId: $routerAmiId}
    },
    serviceInstanceIds: $serviceInstanceIds,
    servicePublicAddresses: $servicePublicAddresses,
    routerInstanceId: $routerInstanceId,
    routerPublicAddress: $routerPublicAddress,
    checks: {
      nodeHealth: "pass",
      cloneIdentityUniqueness: "pass",
      globalIpEgressIdentity: "pass",
      globalIpPeerIsolation: "pass",
      twelveDirectionPeerConnectivity: "pass",
      routerForwardRulesObserved: true,
      routerTrafficControlObserved: true,
      routerStopIsolationAndRecovery: "pass"
    },
    evidence: {
      nftCounterDeltas: $nftCounterDeltas,
      trafficControlCounterDeltas: $trafficControlCounterDeltas
    }
  }' >"${report}.tmp"
mv "${report}.tmp" "${report}"
cp "${report}" "${report_dir}/latest.json.tmp"
mv "${report_dir}/latest.json.tmp" "${report_dir}/latest.json"
record_validation "${service_artifact_id}" "${report_uri}"
record_validation "${router_artifact_id}" "${report_uri}"
printf 'AWS conformance: PASS (%s)\n' "${report}"
