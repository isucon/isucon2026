#!/bin/sh
# Regenerates the nftables fixtures in an unprivileged network namespace.
# router.nft stands in for the table isureal-router renders; block.nft is nftRules.
set -eu
cd "$(dirname "$0")"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
reorder="{ echo 'delete table inet isureal_router'; nft list table inet isureal_router; } >$tmp/router.nft && nft -c -f $tmp/router.nft && nft -f $tmp/router.nft"
unshare -rn sh -c "nft -f router.nft && nft -j list ruleset" >before.json
unshare -rn sh -c "nft -f router.nft && nft -f block.nft && nft -j list ruleset" >partial.json
unshare -rn sh -c "nft -f router.nft && nft -f block.nft && nft -f block.nft && $reorder && nft -j list ruleset" >installed.json
unshare -rn sh -c "nft -f block.nft && nft -f router.nft && nft -j list ruleset" >boot.json
unshare -rn sh -c "nft -f block.nft && nft list table inet isucon_data_proxy_block" >table.txt
