#!/bin/bash
# terraform が付けたタグで develop の app サーバだけを dynamic inventory に出す

export USACLOUD_PROFILE=iqz41186_isucon
export SACLOUD_INVENTORY_FILTER_TAGS="isucon,sys=2026-contest-portal,env=develop"

if ! usacloud config show ${USACLOUD_PROFILE} > /dev/null; then
  echo "Profile '${USACLOUD_PROFILE}' is not found in usacloud config"
  exit 1
fi

cd "$(dirname "$0")/.."
uv run python ./sacloud-ansible-inventory/sacloud_inventory.py "$@"
