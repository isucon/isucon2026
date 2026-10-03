#!/bin/sh
# 第 1 引数でサービスを選ぶ: portal（既定）/ worker / bot / notifyloop。
# それ以外はそのままコマンドとして実行する（デバッグ用に sh などを起動できる）。
set -eu

# ECS injects this secret only into the worker. The SSH client requires a file.
if [ -n "${ISUXPORTAL_ADMIN_SSH_PRIVATE_KEY:-}" ]; then
  umask 077
  export ISUXPORTAL_ISUCON_ADMIN_SSH_PRIVATE_KEY_PATH="${ISUXPORTAL_ISUCON_ADMIN_SSH_PRIVATE_KEY_PATH:-/tmp/isucon_admin.key}"
  printf '%s' "$ISUXPORTAL_ADMIN_SSH_PRIVATE_KEY" >"$ISUXPORTAL_ISUCON_ADMIN_SSH_PRIVATE_KEY_PATH"
  unset ISUXPORTAL_ADMIN_SSH_PRIVATE_KEY
fi

service="${1:-portal}"
if [ "$#" -gt 0 ]; then
  shift
fi

case "${service}" in
portal | worker | bot | notifyloop | migrate)
  exec "/opt/portal/bin/${service}" "$@"
  ;;
*)
  exec "${service}" "$@"
  ;;
esac
