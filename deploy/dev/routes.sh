#!/bin/sh
set -eu
for peer in ${PEER_SUBNETS:?PEER_SUBNETS is required}; do
  ip route replace "$peer" via "${ROUTER_GATEWAY:?ROUTER_GATEWAY is required}"
done
