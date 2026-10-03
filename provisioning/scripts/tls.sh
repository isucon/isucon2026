#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
topology_file="${VM_TOPOLOGY:-${repo_root}/provisioning/config/topology.yml}"

mapfile -t values < <(mise exec -- python - "${topology_file}" <<'PY'
import sys
from pathlib import Path
import yaml

topology = yaml.safe_load(Path(sys.argv[1]).read_text())
portal = topology["portal"]
base = f"team{portal['teamNumber']}-{portal['teamHash']}.{topology['domain']}"
print(topology["environmentId"])
print(f"app.{base}")
for server in ("app1", "app2", "app3"):
    print(f"{server}.{base}")
PY
)

environment_id="${VM_ENV_ID:-${values[0]}}"
if [[ ! "${environment_id}" =~ ^[a-z][a-z0-9-]{2,31}$ ]]; then
  printf 'invalid VM_ENV_ID: %s\n' "${environment_id}" >&2
  exit 2
fi
app_host="${values[1]}"
app1_host="${values[2]}"
app2_host="${values[3]}"
app3_host="${values[4]}"
ca_dir="${repo_root}/provisioning/.state/tls"
tls_dir="${repo_root}/provisioning/.state/${environment_id}/tls"
ca_key="${ca_dir}/local-ca.key"
ca_cert="${ca_dir}/local-ca.crt"
server_key="${tls_dir}/server.key"
server_cert="${tls_dir}/server.crt"
hosts_file="${tls_dir}/hosts"
expected_hosts="$(printf '%s\n' "${app_host}" "${app1_host}" "${app2_host}" "${app3_host}")"

install -d -m 0700 "${ca_dir}" "${tls_dir}"
umask 077

if [[ ! -f "${ca_key}" || ! -f "${ca_cert}" ]]; then
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out "${ca_key}" >/dev/null 2>&1
  openssl req -x509 -new -sha256 -days 3650 \
    -key "${ca_key}" \
    -subj "/CN=ISUReal Local Development CA" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -out "${ca_cert}"
  chmod 0600 "${ca_key}"
  chmod 0644 "${ca_cert}"
fi

current_hosts=""
if [[ -f "${hosts_file}" ]]; then
  current_hosts="$(cat "${hosts_file}")"
fi
if [[ ! -f "${server_key}" || ! -f "${server_cert}" || "${current_hosts}" != "${expected_hosts}" ]]; then
  temporary="$(mktemp -d "${tls_dir}/generate.XXXXXX")"
  trap 'rm -rf -- "${temporary}"' EXIT
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary}/server.key" >/dev/null 2>&1
  openssl req -new -sha256 \
    -key "${temporary}/server.key" \
    -subj "/CN=${app_host}" \
    -out "${temporary}/server.csr"
  cat >"${temporary}/extensions.cnf" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:${app_host},DNS:${app1_host},DNS:${app2_host},DNS:${app3_host}
EOF
  openssl x509 -req -sha256 -days 365 \
    -in "${temporary}/server.csr" \
    -CA "${ca_cert}" \
    -CAkey "${ca_key}" \
    -CAcreateserial \
    -extfile "${temporary}/extensions.cnf" \
    -out "${temporary}/server.crt" >/dev/null 2>&1
  install -m 0600 "${temporary}/server.key" "${server_key}"
  install -m 0644 "${temporary}/server.crt" "${server_cert}"
  printf '%s\n' "${expected_hosts}" >"${hosts_file}"
  chmod 0600 "${hosts_file}"
  rm -rf -- "${temporary}"
  trap - EXIT
fi

printf 'TLS CA certificate: %s\n' "${ca_cert}"
printf 'TLS application URL: https://%s:%s/\n' "${app_host}" \
  "$(mise exec -- python - "${topology_file}" <<'PY'
import sys
from pathlib import Path
import yaml
print(yaml.safe_load(Path(sys.argv[1]).read_text())["clientTlsForward"]["hostPort"])
PY
)"
