#!/bin/bash
set -euo pipefail
instance=${1:?instance ID required}
shift
case "$instance" in
    i-000e45b5dad0a1b39|i-055f64431d3006a5f|i-0a062905e0bb9ebb7|i-03c92149bdc1fcfe1) ;;
    *) echo 'Only Team 12 answer VMs are allowed' >&2; exit 2 ;;
esac
key=${ASTRA_SSH_KEY:-$HOME/.ssh/id_ed25519}
control="/tmp/astra-ssh-$UID"
mkdir -p "$control"
chmod 700 "$control"
options=(-i "$key" -o 'ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id %h'
    -o StrictHostKeyChecking=accept-new -o ConnectTimeout=20
    -o ControlMaster=auto -o ControlPersist=600 -o "ControlPath=$control/%C")
# Reuse an already authenticated control connection. Only a new connection
# needs another ephemeral key authorization through the existing EICE endpoint.
if ! ssh "${options[@]}" -O check "isucon@$instance" >/dev/null 2>&1; then
    aws ec2-instance-connect send-ssh-public-key --instance-id "$instance" --instance-os-user isucon --ssh-public-key "file://$key.pub" >/dev/null
fi
if [ "${1:-}" = --copy ] || [ "${1:-}" = --fetch ]; then
    direction=$1
    shift
    source=${1:?source required}
    target=${2:?target required}
    if [ "$direction" = --fetch ]; then
        exec scp -q "${options[@]}" "isucon@$instance:$source" "$target"
    fi
    exec scp -q "${options[@]}" "$source" "isucon@$instance:$target"
fi
exec ssh "${options[@]}" -o ServerAliveInterval=30 "isucon@$instance" "$@"
