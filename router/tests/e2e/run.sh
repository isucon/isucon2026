#!/usr/bin/env bash
set -Eeuo pipefail

export COMPOSE_PROGRESS="${COMPOSE_PROGRESS:-plain}"
export BUILDKIT_PROGRESS="${BUILDKIT_PROGRESS:-plain}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd -- "$script_dir/../../.." && pwd)"
compose_file="$repository_root/router/compose.e2e.yaml"
project_name="isureal-router-e2e"
artifact_dir="$repository_root/router/.e2e-artifacts"
mkdir -p "$artifact_dir"

compose=(docker compose --project-name "$project_name" --file "$compose_file" --profile test)
result=0

cleanup() {
  result=$?
  trap - EXIT INT TERM
  if (( result != 0 )); then
    echo "router e2e: FAILED; collecting diagnostics" >&2
    "${compose[@]}" ps --all >&2 || true
    "${compose[@]}" logs --no-color --timestamps >"$artifact_dir/compose.log" 2>&1 || true
    if [[ "${ROUTER_E2E_KEEP_ON_FAILURE:-0}" == "1" ]]; then
      echo "router e2e: resources kept for project $project_name" >&2
      exit "$result"
    fi
  fi
  "${compose[@]}" down --volumes --remove-orphans --timeout 5 >/dev/null 2>&1 || true
  exit "$result"
}
trap cleanup EXIT INT TERM

echo "router e2e: preparing isolated project $project_name"
"${compose[@]}" down --volumes --remove-orphans --timeout 5 >/dev/null 2>&1 || true
"${compose[@]}" build portal router haproxy digest app1 app2 app3 db1 e2e
"${compose[@]}" up --detach --wait --wait-timeout 90 portal router app1 app2 app3 db1 haproxy digest

echo "router e2e: validating routing, shaping, and dynamic updates"
"${compose[@]}" run --rm --no-deps e2e full

echo "router e2e: stopping isureal-digest; the apps must answer the benchmark"
"${compose[@]}" stop --timeout 5 digest
"${compose[@]}" run --rm --no-deps e2e digest-fallback
"${compose[@]}" up --detach --wait --wait-timeout 90 digest

echo "router e2e: injecting Portal restart"
"${compose[@]}" restart --timeout 5 portal
"${compose[@]}" run --rm --no-deps e2e verify-initial

echo "router e2e: injecting HAProxy-only restart"
"${compose[@]}" stop --timeout 5 haproxy
if "${compose[@]}" exec --no-TTY router wget -q -O /dev/null http://127.0.0.1:18081/readyz; then
  echo "router e2e: Router remained ready while HAProxy was stopped" >&2
  exit 1
fi
"${compose[@]}" up --detach --wait --wait-timeout 90 haproxy
"${compose[@]}" run --rm --no-deps e2e verify-initial

echo "router e2e: injecting Router and HAProxy restart"
"${compose[@]}" stop --timeout 5 digest haproxy router
"${compose[@]}" up --detach --wait --wait-timeout 90 router haproxy digest
"${compose[@]}" run --rm --no-deps e2e verify-initial

echo "router e2e: proving peer isolation while Router is down"
peer_pairs=(
  "app1 172.31.2.10"
  "app1 172.31.3.10"
  "app2 172.31.1.10"
  "app2 172.31.3.10"
  "app3 172.31.1.10"
  "app3 172.31.2.10"
  "app1 172.31.4.10"
  "app2 172.31.4.10"
  "app3 172.31.4.10"
  "db1 172.31.1.10"
  "db1 172.31.2.10"
  "db1 172.31.3.10"
)
for peer_pair in "${peer_pairs[@]}"; do
  read -r source target <<<"$peer_pair"
  "${compose[@]}" exec --no-TTY "$source" wget -q -T 5 -O /dev/null "http://$target:8080/healthz"
done
"${compose[@]}" stop --timeout 5 digest haproxy router
for peer_pair in "${peer_pairs[@]}"; do
  read -r source target <<<"$peer_pair"
  if "${compose[@]}" exec --no-TTY "$source" wget -q -T 2 -O /dev/null "http://$target:8080/healthz"; then
    echo "router e2e: $source unexpectedly reached $target while Router was stopped" >&2
    exit 1
  fi
done
echo "router e2e: PASS all twelve peer directions are isolated"

"${compose[@]}" up --detach --wait --wait-timeout 90 router haproxy digest
"${compose[@]}" run --rm --no-deps e2e verify-initial

echo "router e2e: PASS"
