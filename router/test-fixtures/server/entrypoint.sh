#!/bin/sh
set -eu

old_ifs=$IFS
IFS=,
set -- ${FIXTURE_PEER_SUBNETS:?FIXTURE_PEER_SUBNETS is required}
IFS=$old_ifs

for peer_subnet in "$@"; do
  ip route replace "$peer_subnet" via "${FIXTURE_ROUTER_IP:?FIXTURE_ROUTER_IP is required}"
done

exec /usr/local/bin/router-test-server
