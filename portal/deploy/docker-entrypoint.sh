#!/bin/sh
# 第 1 引数でサービスを選ぶ: portal（既定）/ worker / bot / notifyloop。
# それ以外はそのままコマンドとして実行する（デバッグ用に sh などを起動できる）。
set -eu

service="${1:-portal}"
if [ "$#" -gt 0 ]; then
  shift
fi

case "${service}" in
  portal|worker|bot|notifyloop)
    exec "/opt/portal/bin/${service}" "$@"
    ;;
  *)
    exec "${service}" "$@"
    ;;
esac
