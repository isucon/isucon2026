#!/bin/bash
set -euo pipefail
routes.sh
isureal &
api_pid=$!
nginx -g 'daemon off;' &
nginx_pid=$!
cleanup() {
  trap - EXIT TERM INT
  kill -TERM "$api_pid" "$nginx_pid" 2>/dev/null || true
  wait "$api_pid" "$nginx_pid" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 0' TERM INT
wait -n "$api_pid" "$nginx_pid"
