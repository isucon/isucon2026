#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
topology_file="${VM_TOPOLOGY:-${repo_root}/provisioning/config/topology.yml}"
if [[ "${topology_file}" != /* ]]; then
  topology_file="${repo_root}/${topology_file}"
fi

benchmark_config="$({
  cd "${repo_root}"
  mise exec -- python - "${topology_file}" <<'PY'
import sys
from pathlib import Path

import yaml

from provisioning.scripts.preflight import benchmark_endpoint

topology = yaml.safe_load(Path(sys.argv[1]).read_text())
target, host = benchmark_endpoint(topology)
print(f"{target}\t{host}")
PY
})"
IFS=$'\t' read -r target request_host <<<"${benchmark_config}"
if [[ -z "${target}" || -z "${request_host}" ]]; then
  printf 'failed to resolve Vagrant benchmark endpoint from %s\n' "${topology_file}" >&2
  exit 2
fi

printf 'Vagrant benchmark: target=%s host=%s\n' "${target}" "${request_host}" >&2
cd "${repo_root}"
exec mise exec -- go -C bench run . -target "${target}" -host "${request_host}" "$@"
